package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

func TestFlushRequeuesDeadLettersWhenAsked(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	if err := config.Save(home, testConfig(databoxSink())); err != nil {
		t.Fatalf("save config: %v", err)
	}
	at := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	ev := store.Event{EventType: quota.EventChange, Snapshot: quota.Snapshot{SchemaVersion: 1, EventID: "evt-1", CapturedAt: at}, Delivery: map[string]store.DeliveryState{"databox-main": {NextAttemptAt: at}}}
	if _, err := store.WritePending(home, ev); err != nil {
		t.Fatalf("spool: %v", err)
	}
	list, _ := store.ListPending(home)
	if err := store.DeadLetter(home, list[0], "http 401", at); err != nil {
		t.Fatalf("dead-letter: %v", err)
	}
	var stdout bytes.Buffer
	err := runFlush(t.Context(), []string{"--requeue"}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	pending, dead, _ := store.Counts(home)
	if err != nil || pending != 1 || dead != 0 || stdout.String() != "requeued 1 events\ndelivered 0, retried 0, dead-lettered 0, quarantined 0\n" {
		t.Fatalf("err=%v pending=%d dead=%d stdout=%q", err, pending, dead, stdout.String())
	}
}

func TestFlushWithoutConfigFails(t *testing.T) {
	t.Setenv(store.HomeEnv, t.TempDir())
	err := runFlush(t.Context(), nil, BuildInfo{}, IO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("got nil, want an error for a missing config")
	}
}
