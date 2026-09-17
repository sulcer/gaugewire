package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/quota"
)

func spoolHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	return home
}

func event(id string, capturedAt time.Time) Event {
	return Event{
		EventType: quota.EventChange,
		Snapshot:  quota.Snapshot{SchemaVersion: 1, EventID: id, CapturedAt: capturedAt},
		Delivery:  map[string]DeliveryState{"databox-main": {NextAttemptAt: capturedAt}},
	}
}

func TestWritePendingNamesTheFileByCaptureTimeAndEventID(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 30, 0, 123_000_000, time.UTC)
	got, err := WritePending(home, event("evt-1", captured))
	if err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	want := filepath.Join(home, PendingDir, "1789659000123-evt-1.json")
	if got != want {
		t.Fatalf("path %q, want %q", got, want)
	}
}

func TestListPendingReturnsEventsInCaptureOrder(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	base := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	later := event("evt-later", base.Add(time.Hour))
	earlier := event("evt-earlier", base)
	if _, err := WritePending(home, later); err != nil {
		t.Fatalf("write later: %v", err)
	}
	if _, err := WritePending(home, earlier); err != nil {
		t.Fatalf("write earlier: %v", err)
	}
	got, err := ListPending(home)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	want := []PendingEvent{
		{Path: filepath.Join(home, PendingDir, "1789657200000-evt-earlier.json"), Event: earlier},
		{Path: filepath.Join(home, PendingDir, "1789660800000-evt-later.json"), Event: later},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("pending mismatch (-want +got):\n%s", diff)
	}
}

func TestListPendingIgnoresTemporaryFiles(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	if err := os.WriteFile(filepath.Join(home, PendingDir, ".tmp-abc"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	got, err := ListPending(home)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d events, %v; want 0, nil", len(got), err)
	}
}

func TestUpdatePendingRewritesDeliveryState(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	if _, err := WritePending(home, event("evt-1", captured)); err != nil {
		t.Fatalf("write: %v", err)
	}
	list, err := ListPending(home)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	pe := list[0]
	pe.Event.Delivery["databox-main"] = DeliveryState{Attempts: 1, NextAttemptAt: captured.Add(5 * time.Second), LastError: "timeout", LastErrorCode: ""}
	if updateErr := UpdatePending(pe); updateErr != nil {
		t.Fatalf("UpdatePending: %v", updateErr)
	}
	again, err := ListPending(home)
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	if diff := cmp.Diff([]PendingEvent{pe}, again); diff != "" {
		t.Fatalf("pending mismatch (-want +got):\n%s", diff)
	}
}

func TestDeletePendingRemovesTheFile(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	if _, err := WritePending(home, event("evt-1", time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("write: %v", err)
	}
	list, _ := ListPending(home)
	if err := DeletePending(list[0]); err != nil {
		t.Fatalf("DeletePending: %v", err)
	}
	pending, dead, err := Counts(home)
	if err != nil || pending != 0 || dead != 0 {
		t.Fatalf("counts %d/%d, %v; want 0/0, nil", pending, dead, err)
	}
}

func TestDeadLetterMovesTheEventWithAReason(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	now := captured.Add(time.Minute)
	if _, err := WritePending(home, event("evt-1", captured)); err != nil {
		t.Fatalf("write: %v", err)
	}
	list, _ := ListPending(home)
	if err := DeadLetter(home, list[0], "http 401 invalid_api_key", now); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}
	pending, dead, err := Counts(home)
	if err != nil || pending != 0 || dead != 1 {
		t.Fatalf("counts %d/%d, %v; want 0/1, nil", pending, dead, err)
	}
}

func TestRequeueMovesDeadLettersBackWithFreshDelivery(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	ev := event("evt-1", captured)
	ev.Delivery["databox-main"] = DeliveryState{Attempts: 3, NextAttemptAt: captured, LastError: "boom", LastErrorCode: "forbidden"}
	if _, err := WritePending(home, ev); err != nil {
		t.Fatalf("write: %v", err)
	}
	list, _ := ListPending(home)
	if err := DeadLetter(home, list[0], "http 403 forbidden", captured.Add(time.Minute)); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}
	now := captured.Add(time.Hour)
	n, err := Requeue(home, now)
	if err != nil || n != 1 {
		t.Fatalf("Requeue: n=%d err=%v; want 1, nil", n, err)
	}
	got, err := ListPending(home)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	wantEvent := event("evt-1", captured)
	wantEvent.Delivery["databox-main"] = DeliveryState{NextAttemptAt: now}
	want := []PendingEvent{{Path: filepath.Join(home, PendingDir, "1789657200000-evt-1.json"), Event: wantEvent}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("pending mismatch (-want +got):\n%s", diff)
	}
}
