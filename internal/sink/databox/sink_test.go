package databox

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/sink"
)

// memIngestions is an in-memory IngestionStore.
type memIngestions struct {
	mu       sync.Mutex
	recs     map[string]sink.Ingestion
	failLoad error
	failSave error
}

func (m *memIngestions) LoadIngestion(id string) (sink.Ingestion, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failLoad != nil {
		return sink.Ingestion{}, false, m.failLoad
	}
	r, ok := m.recs[id]
	return r, ok, nil
}

func (m *memIngestions) SaveIngestion(id string, ing sink.Ingestion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failSave != nil {
		return m.failSave
	}
	if m.recs == nil {
		m.recs = map[string]sink.Ingestion{}
	}
	m.recs[id] = ing
	return nil
}

var sendAt = time.Date(2026, 9, 17, 15, 30, 5, 0, time.UTC)

func newSink(t *testing.T, f *fake, ings *memIngestions) *Sink {
	t.Helper()
	s, err := New("databox-main", client(t, f), Datasets{History: "ds-hist", Current: "ds-cur"}, ings, func() time.Time { return sendAt }, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func deliveries(ids ...string) []sink.Delivery {
	out := make([]sink.Delivery, 0, len(ids))
	for i, id := range ids {
		s := observedSnapshot(id)
		s.CapturedAt = capturedAt.Add(time.Duration(i) * time.Minute)
		out = append(out, sink.Delivery{EventType: quota.EventChange, Snapshot: s})
	}
	return out
}

func paths(calls []call) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Method+" "+c.Path)
	}
	return out
}

func TestPublishBatchWritesHistoryThenCurrentAndRecordsIds(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v1/datasets/ds-hist/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`)
	f.on("POST", "/v1/datasets/ds-cur/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-c","message":"ok"}`)
	ings := &memIngestions{}
	err := newSink(t, f, ings).PublishBatch(t.Context(), deliveries("evt-1", "evt-2"))
	newest := capturedAt.Add(time.Minute)
	got := struct {
		err   bool
		paths []string
		rec   sink.Ingestion
	}{err != nil, paths(f.seen()), ings.recs["databox-main"]}
	want := struct {
		err   bool
		paths []string
		rec   sink.Ingestion
	}{false, []string{"POST /v1/datasets/ds-hist/data", "POST /v1/datasets/ds-cur/data"}, sink.Ingestion{Current: "ing-c", History: "ing-h", CurrentCapturedAt: &newest, At: sendAt}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestPublishBatchSkipsCurrentWhenOlderThanRecorded(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v1/datasets/ds-hist/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-h2","message":"ok"}`)
	later := capturedAt.Add(time.Hour)
	ings := &memIngestions{recs: map[string]sink.Ingestion{"databox-main": {Current: "ing-c1", History: "ing-h1", CurrentCapturedAt: &later, At: sendAt.Add(-time.Hour)}}}
	err := newSink(t, f, ings).PublishBatch(t.Context(), deliveries("evt-old"))
	got := struct {
		err   bool
		paths []string
		rec   sink.Ingestion
	}{err != nil, paths(f.seen()), ings.recs["databox-main"]}
	want := struct {
		err   bool
		paths []string
		rec   sink.Ingestion
	}{false, []string{"POST /v1/datasets/ds-hist/data"}, sink.Ingestion{Current: "ing-c1", History: "ing-h2", CurrentCapturedAt: &later, At: sendAt}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestPublishBatchSkipsCurrentOnAnEqualCaptureTime(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v1/datasets/ds-hist/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-h2","message":"ok"}`)
	equal := capturedAt
	ings := &memIngestions{recs: map[string]sink.Ingestion{"databox-main": {Current: "ing-c1", History: "ing-h1", CurrentCapturedAt: &equal, At: sendAt.Add(-time.Hour)}}}
	err := newSink(t, f, ings).PublishBatch(t.Context(), deliveries("evt-same"))
	got := struct {
		err   bool
		paths []string
		rec   sink.Ingestion
	}{err != nil, paths(f.seen()), ings.recs["databox-main"]}
	want := struct {
		err   bool
		paths []string
		rec   sink.Ingestion
	}{false, []string{"POST /v1/datasets/ds-hist/data"}, sink.Ingestion{Current: "ing-c1", History: "ing-h2", CurrentCapturedAt: &equal, At: sendAt}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestPublishBatchReturnsTheClassifiedHistoryError(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v1/datasets/ds-hist/data", 429, `{"requestId":"r","status":"error","errors":[{"code":"rate_limited","message":"slow down","field":"","type":"limit"}]}`)
	ings := &memIngestions{}
	err := newSink(t, f, ings).PublishBatch(t.Context(), deliveries("evt-1"))
	class, code := sink.Classify(err)
	got := struct {
		class sink.Class
		code  string
		calls int
		saved bool
	}{class, code, len(f.seen()), len(ings.recs) > 0}
	want := struct {
		class sink.Class
		code  string
		calls int
		saved bool
	}{sink.Retryable, "rate_limited", 1, false}
	if err == nil || got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

func TestPublishBatchStillSucceedsWhenTheRecordCannotBeSaved(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v1/datasets/ds-hist/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`)
	f.on("POST", "/v1/datasets/ds-cur/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-c","message":"ok"}`)
	ings := &memIngestions{failSave: errors.New("lock timeout")}
	err := newSink(t, f, ings).PublishBatch(t.Context(), deliveries("evt-1"))
	got := struct {
		err   bool
		paths []string
		saved bool
	}{err != nil, paths(f.seen()), len(ings.recs) > 0}
	want := struct {
		err   bool
		paths []string
		saved bool
	}{false, []string{"POST /v1/datasets/ds-hist/data", "POST /v1/datasets/ds-cur/data"}, false}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestPublishBatchPostsCurrentWhenTheRecordCannotBeLoaded(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v1/datasets/ds-hist/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`)
	f.on("POST", "/v1/datasets/ds-cur/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-c","message":"ok"}`)
	ings := &memIngestions{failLoad: errors.New("state.json is not valid")}
	err := newSink(t, f, ings).PublishBatch(t.Context(), deliveries("evt-1"))
	got := struct {
		err   bool
		paths []string
		rec   sink.Ingestion
	}{err != nil, paths(f.seen()), ings.recs["databox-main"]}
	want := struct {
		err   bool
		paths []string
		rec   sink.Ingestion
	}{false, []string{"POST /v1/datasets/ds-hist/data", "POST /v1/datasets/ds-cur/data"}, sink.Ingestion{Current: "ing-c", History: "ing-h", CurrentCapturedAt: &capturedAt, At: sendAt}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestNewRefusesMissingDatasetIds(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	_, err := New("databox-main", client(t, f), Datasets{History: "", Current: "ds-cur"}, &memIngestions{}, time.Now, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("got nil, want an error for a missing dataset id")
	}
}
