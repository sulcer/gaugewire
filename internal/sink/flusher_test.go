package sink

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

// fakeSink returns the scripted errors in order, then nil, and records every batch.
type fakeSink struct {
	id      string
	script  []error
	batches [][]string
}

func (f *fakeSink) ID() string { return f.id }

func (f *fakeSink) PublishBatch(_ context.Context, snapshots []quota.Snapshot) error {
	ids := make([]string, 0, len(snapshots))
	for _, s := range snapshots {
		ids = append(ids, s.EventID)
	}
	f.batches = append(f.batches, ids)
	if len(f.script) == 0 {
		return nil
	}
	err := f.script[0]
	f.script = f.script[1:]
	return err
}

var now = time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)

func flusherHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	return home
}

func spool(t *testing.T, home, id string, at time.Time, sinks ...string) {
	t.Helper()
	ev := store.Event{EventType: quota.EventChange, Snapshot: quota.Snapshot{SchemaVersion: 1, EventID: id, CapturedAt: at}, Delivery: map[string]store.DeliveryState{}}
	for _, s := range sinks {
		ev.Delivery[s] = store.DeliveryState{NextAttemptAt: at}
	}
	if _, err := store.WritePending(home, ev); err != nil {
		t.Fatalf("spool %s: %v", id, err)
	}
}

func run(t *testing.T, home string, sinks ...Sink) (Result, error) {
	t.Helper()
	f := Flusher{Home: home, Sinks: sinks, Now: func() time.Time { return now }, Random: func() float64 { return 0.5 }, Logger: logging.Discard()}
	return f.Run(t.Context())
}

type spoolState struct {
	result    Result
	runErr    bool
	pending   map[string]map[string]store.DeliveryState
	dead      []string
	batches   [][]string
	lastFlush *store.FlushRecord
}

func snapshotSpool(t *testing.T, home string, res Result, err error, sinks ...*fakeSink) spoolState {
	t.Helper()
	st := spoolState{result: res, runErr: err != nil, pending: map[string]map[string]store.DeliveryState{}}
	pending, listErr := store.ListPending(home)
	if listErr != nil {
		t.Fatalf("ListPending: %v", listErr)
	}
	for _, pe := range pending {
		st.pending[pe.Event.Snapshot.EventID] = pe.Event.Delivery
	}
	entries, _ := os.ReadDir(filepath.Join(home, store.DeadLetterDir))
	for _, e := range entries {
		st.dead = append(st.dead, e.Name())
	}
	sort.Strings(st.dead)
	for _, s := range sinks {
		st.batches = append(st.batches, s.batches...)
	}
	state, _ := store.LoadState(home)
	st.lastFlush = state.LastFlush
	return st
}

func TestRunDeliversDueEventsAndDeletesThem(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-2*time.Minute), "a")
	spool(t, home, "evt-2", now.Add(-time.Minute), "a")
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	want := spoolState{
		result:    Result{Delivered: 2},
		pending:   map[string]map[string]store.DeliveryState{},
		batches:   [][]string{{"evt-1", "evt-2"}},
		lastFlush: &store.FlushRecord{At: now, OK: true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunRetriesWithBackoffAndStopsTheSink(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-2*time.Minute), "a")
	spool(t, home, "evt-2", now.Add(-time.Minute), "a")
	a := &fakeSink{id: "a", script: []error{NewRetryable("rate_limited", errors.New("429"))}}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	retried := store.DeliveryState{Attempts: 1, NextAttemptAt: now.Add(5 * time.Second), LastError: "retryable (rate_limited): 429", LastErrorCode: "rate_limited"}
	want := spoolState{
		result:    Result{Retried: 2},
		runErr:    true,
		pending:   map[string]map[string]store.DeliveryState{"evt-1": {"a": retried}, "evt-2": {"a": retried}},
		batches:   [][]string{{"evt-1", "evt-2"}},
		lastFlush: &store.FlushRecord{At: now, OK: false, Error: "sink a: retryable (rate_limited): 429"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunDeadLettersPermanentFailuresAndContinues(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-2*time.Minute), "a")
	a := &fakeSink{id: "a", script: []error{NewPermanent("invalid_api_key", errors.New("401"))}}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	want := spoolState{
		result:    Result{DeadLettered: 1},
		pending:   map[string]map[string]store.DeliveryState{},
		dead:      []string{"1789657080000-evt-1.json"},
		batches:   [][]string{{"evt-1"}},
		lastFlush: &store.FlushRecord{At: now, OK: true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunDeadLetterTakesTheEventOutOfCirculation(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-time.Minute), "a", "b")
	a := &fakeSink{id: "a", script: []error{NewPermanent("invalid_api_key", errors.New("401"))}}
	b := &fakeSink{id: "b"}
	res, err := run(t, home, a, b)
	got := snapshotSpool(t, home, res, err, a, b)
	want := spoolState{
		result:    Result{DeadLettered: 1},
		pending:   map[string]map[string]store.DeliveryState{},
		dead:      []string{"1789657140000-evt-1.json"},
		batches:   [][]string{{"evt-1"}},
		lastFlush: &store.FlushRecord{At: now, OK: true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunSkipsEventsThatAreNotDueOrNotTargeted(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-future", now.Add(time.Hour), "a")
	spool(t, home, "evt-other", now.Add(-time.Minute), "b")
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	want := spoolState{
		result: Result{},
		pending: map[string]map[string]store.DeliveryState{
			"evt-future": {"a": {NextAttemptAt: now.Add(time.Hour)}},
			"evt-other":  {"b": {NextAttemptAt: now.Add(-time.Minute)}},
		},
		lastFlush: &store.FlushRecord{At: now, OK: true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunKeepsAnEventUntilEverySinkAccepted(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-time.Minute), "a", "b")
	a := &fakeSink{id: "a"}
	b := &fakeSink{id: "b", script: []error{NewRetryable("", errors.New("timeout"))}}
	res, err := run(t, home, a, b)
	got := snapshotSpool(t, home, res, err, a, b)
	want := spoolState{
		result:    Result{Delivered: 1, Retried: 1},
		runErr:    true,
		pending:   map[string]map[string]store.DeliveryState{"evt-1": {"b": {Attempts: 1, NextAttemptAt: now.Add(5 * time.Second), LastError: "retryable: timeout"}}},
		batches:   [][]string{{"evt-1"}, {"evt-1"}},
		lastFlush: &store.FlushRecord{At: now, OK: false, Error: "sink b: retryable: timeout"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunChunksAtMaxBatch(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	for i := range MaxBatch + 1 {
		spool(t, home, fmt.Sprintf("evt-%03d", i), now.Add(time.Duration(i-200)*time.Second), "a")
	}
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	type chunkOutcome struct {
		delivered int
		ok        bool
		sizes     []int
	}
	sizes := make([]int, 0, len(a.batches))
	for _, b := range a.batches {
		sizes = append(sizes, len(b))
	}
	got := chunkOutcome{delivered: res.Delivered, ok: err == nil, sizes: sizes}
	want := chunkOutcome{delivered: MaxBatch + 1, ok: true, sizes: []int{100, 1}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(chunkOutcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunExitsWhenAnotherFlusherHoldsTheLock(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-time.Minute), "a")
	unlock, held, err := store.TryLock(filepath.Join(home, FlushLockFile))
	if err != nil || !held {
		t.Fatalf("pre-lock: held=%v err=%v", held, err)
	}
	defer func() { _ = unlock() }()
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	type lockOutcome struct {
		skipped bool
		calls   int
		err     bool
	}
	got := lockOutcome{skipped: res.Skipped, calls: len(a.batches), err: err != nil}
	want := lockOutcome{skipped: true, calls: 0, err: false}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(lockOutcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunQuarantinesAnUnreadableFileFirst(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-time.Minute), "a")
	if err := os.WriteFile(filepath.Join(home, store.PendingDir, "1789657000000-evt-bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	want := spoolState{
		result:    Result{Delivered: 1, Quarantined: 1},
		pending:   map[string]map[string]store.DeliveryState{},
		dead:      []string{"1789657000000-evt-bad.json.unreadable"},
		batches:   [][]string{{"evt-1"}},
		lastFlush: &store.FlushRecord{At: now, OK: true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}
