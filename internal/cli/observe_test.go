package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/store"
)

var observedAt = time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "statusline", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func testConfig(sinks ...config.Sink) config.Config {
	c := config.Default()
	c.Node = config.Identity{ID: "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", Alias: "mac-mini-01"}
	c.Account = config.Identity{ID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", Alias: "claude-01"}
	c.Sinks = sinks
	return c
}

func databoxSink() config.Sink {
	return config.Sink{ID: "databox-main", Type: config.SinkTypeDatabox, Enabled: true}
}

type observed struct {
	result   observeResult
	pending  int
	dead     int
	statusOK bool
}

func snapshotObserve(t *testing.T, home string, res observeResult) observed {
	t.Helper()
	pending, dead, err := store.Counts(home)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	state, err := store.LoadState(home)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return observed{result: res, pending: pending, dead: dead, statusOK: state.LastObservedAt != nil}
}

func TestObservePublishesTheFirstObservationAndSpools(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := observe(t.Context(), home, testConfig(databoxSink()), fixture(t, "full.json"), observedAt, BuildInfo{Version: "1.0.0"}, logging.Discard())
	got := snapshotObserve(t, home, res)
	got.result.EventID = ""
	want := observed{result: observeResult{Published: true, Spawn: true}, pending: 1, statusOK: true}
	if got != want || res.EventID == "" {
		t.Fatalf("got %+v (eventId %q), want %+v with a non-empty eventId", got, res.EventID, want)
	}
}

func TestObserveDoesNotPublishTheSameStateTwice(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := testConfig(databoxSink())
	first := observe(t.Context(), home, cfg, fixture(t, "full.json"), observedAt, BuildInfo{}, logging.Discard())
	second := observe(t.Context(), home, cfg, fixture(t, "full.json"), observedAt.Add(time.Minute), BuildInfo{}, logging.Discard())
	got := snapshotObserve(t, home, second)
	got.result.EventID = ""
	want := observed{result: observeResult{Published: false, Spawn: true}, pending: 1, statusOK: true}
	if !first.Published || got != want {
		t.Fatalf("first=%+v second=%+v, want first published and second only spawning for due work", first, got)
	}
}

func TestObserveWithoutSinksPublishesButSpoolsNothing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := observe(t.Context(), home, testConfig(), fixture(t, "full.json"), observedAt, BuildInfo{}, logging.Discard())
	got := snapshotObserve(t, home, res)
	got.result.EventID = ""
	want := observed{result: observeResult{Published: true, Spawn: false}, pending: 0, statusOK: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestObserveSkipsAnUnsupportedVersion(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := observe(t.Context(), home, testConfig(databoxSink()), fixture(t, "old-version.json"), observedAt, BuildInfo{}, logging.Discard())
	got := snapshotObserve(t, home, res)
	want := observed{result: observeResult{}, statusOK: false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestObserveFailsOpenWhenTheLockIsHeld(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	unlock, err := store.Lock(t.Context(), filepath.Join(home, store.StateLockFile), time.Second)
	if err != nil {
		t.Fatalf("pre-lock: %v", err)
	}
	defer func() { _ = unlock() }()
	res := observe(t.Context(), home, testConfig(databoxSink()), fixture(t, "full.json"), observedAt, BuildInfo{}, logging.Discard())
	got := snapshotObserve(t, home, res)
	want := observed{result: observeResult{}, statusOK: false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestObserveUnderConcurrencyPublishesOnce(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := testConfig(databoxSink())
	payload := fixture(t, "full.json")
	const sessions = 20
	var wg sync.WaitGroup
	results := make([]observeResult, sessions)
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = observe(t.Context(), home, cfg, payload, observedAt, BuildInfo{}, logging.Discard())
		}()
	}
	wg.Wait()
	published := 0
	for _, r := range results {
		if r.Published {
			published++
		}
	}
	pending, _, err := store.Counts(home)
	if err != nil || published != 1 || pending != 1 {
		t.Fatalf("published=%d pending=%d err=%v; want exactly one of each", published, pending, err)
	}
}
