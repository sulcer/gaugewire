package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

// fakeAPI answers the two ingestion endpoints and counts the requests it saw.
// The history endpoint is scripted so a test can make the first call fail.
func fakeAPI(t *testing.T, historyStatus int, historyBody string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/datasets/ds-hist/data":
			w.WriteHeader(historyStatus)
			_, _ = w.Write([]byte(historyBody))
		case "/v1/datasets/ds-cur/data":
			_, _ = w.Write([]byte(`{"requestId":"r","status":"success","ingestionId":"ing-c","message":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"requestId":"r","status":"error","errors":[{"code":"not_found","message":"no route","field":"","type":"routing"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func databoxHome(t *testing.T, baseURL string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	t.Setenv("GW_TEST_DATABOX_KEY", "test-key-abc")
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	cfg := testConfig(config.Sink{
		ID: "databox-main", Type: config.SinkTypeDatabox, Enabled: true, BaseURL: baseURL,
		AccountID: 123456, DataSourceID: 4754489, CurrentDatasetID: "ds-cur", HistoryDatasetID: "ds-hist",
		Credentials: config.Credentials{APIKeyEnv: "GW_TEST_DATABOX_KEY"},
	})
	if err := config.Save(home, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	at := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	ev := store.Event{EventType: quota.EventChange, Snapshot: quota.NewSnapshot(cfg.QuotaIdentity("darwin", "1.0.0"), quota.NewState(), "evt-1", at), Delivery: map[string]store.DeliveryState{"databox-main": {NextAttemptAt: at}}}
	if _, err := store.WritePending(home, ev); err != nil {
		t.Fatalf("spool: %v", err)
	}
	return home
}

func TestFlushDeliversToDataboxAndClearsTheSpool(t *testing.T) {
	srv, requests := fakeAPI(t, http.StatusOK, `{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`)
	home := databoxHome(t, srv.URL)
	var stdout bytes.Buffer
	err := runFlush(t.Context(), nil, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	pending, dead, _ := store.Counts(home)
	state, _ := store.LoadState(home)
	got := struct {
		err      bool
		out      string
		pending  int
		dead     int
		history  string
		current  string
		requests int64
	}{err != nil, stdout.String(), pending, dead, state.LastIngestion["databox-main"].History, state.LastIngestion["databox-main"].Current, requests.Load()}
	want := struct {
		err      bool
		out      string
		pending  int
		dead     int
		history  string
		current  string
		requests int64
	}{false, "delivered 1, retried 0, dead-lettered 0, quarantined 0\n", 0, 0, "ing-h", "ing-c", 2}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFlushDeadLettersOnAnInvalidKeyWithoutLoggingIt(t *testing.T) {
	srv, _ := fakeAPI(t, http.StatusUnauthorized, `{"requestId":"r","status":"error","errors":[{"code":"invalid_api_key","message":"key rejected","field":"","type":"auth"}]}`)
	home := databoxHome(t, srv.URL)
	var stdout bytes.Buffer
	err := runFlush(t.Context(), nil, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	pending, dead, _ := store.Counts(home)
	log, _ := os.ReadFile(filepath.Join(home, store.LogsDir, logging.FileName))
	got := struct {
		err     bool
		out     string
		pending int
		dead    int
		leaked  bool
	}{err != nil, stdout.String(), pending, dead, strings.Contains(string(log), "test-key-abc")}
	want := struct {
		err     bool
		out     string
		pending int
		dead    int
		leaked  bool
	}{false, "delivered 0, retried 0, dead-lettered 1, quarantined 0\n", 0, 1, false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFlushRetriesOnRateLimit(t *testing.T) {
	srv, _ := fakeAPI(t, http.StatusTooManyRequests, `{"requestId":"r","status":"error","errors":[{"code":"rate_limited","message":"slow down","field":"","type":"limit"}]}`)
	home := databoxHome(t, srv.URL)
	var stdout bytes.Buffer
	err := runFlush(t.Context(), nil, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	pending, _ := store.ListPending(home)
	if len(pending) != 1 {
		t.Fatalf("pending events %d, want exactly one still spooled", len(pending))
	}
	got := struct {
		err      bool
		out      string
		attempts int
		code     string
	}{err != nil, stdout.String(), pending[0].Event.Delivery["databox-main"].Attempts, pending[0].Event.Delivery["databox-main"].LastErrorCode}
	want := struct {
		err      bool
		out      string
		attempts int
		code     string
	}{true, "delivered 0, retried 1, dead-lettered 0, quarantined 0\n", 1, "rate_limited"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFlushFailsWhenASinkHasNoKey(t *testing.T) {
	srv, requests := fakeAPI(t, http.StatusOK, `{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`)
	home := databoxHome(t, srv.URL)
	t.Setenv("GW_TEST_DATABOX_KEY", "")
	var stdout bytes.Buffer
	err := runFlush(t.Context(), nil, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	got := sinkSetupOutcome(t, home, err, requests.Load())
	want := sinkSetupFailure{
		err: true, pending: 1, reported: true,
		lastFlushOK: false, lastFlushError: "sink databox-main not built: no Databox API key: pass --api-key-file to gaugewire databox bootstrap, or set the environment variable named by credentials.apiKeyEnv (default DATABOX_API_KEY)",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFlushFailsWhenASinkHasNoDatasetIDs(t *testing.T) {
	srv, requests := fakeAPI(t, http.StatusOK, `{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`)
	home := databoxHome(t, srv.URL)
	cfg, _ := config.Load(home)
	cfg.Sinks[0].HistoryDatasetID, cfg.Sinks[0].CurrentDatasetID = "", ""
	if err := config.Save(home, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	var stdout bytes.Buffer
	err := runFlush(t.Context(), nil, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	got := sinkSetupOutcome(t, home, err, requests.Load())
	want := sinkSetupFailure{
		err: true, pending: 1, reported: true,
		lastFlushOK: false, lastFlushError: "sink databox-main not built: both dataset ids are required; run gaugewire databox bootstrap",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// sinkSetupFailure is what a flush leaves behind when a configured sink
// could not be built: the run fails, the event stays pending, nothing is sent.
type sinkSetupFailure struct {
	err            bool
	pending        int
	dead           int
	requests       int64
	reported       bool
	lastFlushOK    bool
	lastFlushError string
}

func sinkSetupOutcome(t *testing.T, home string, err error, requests int64) sinkSetupFailure {
	t.Helper()
	pending, dead, _ := store.Counts(home)
	log, _ := os.ReadFile(filepath.Join(home, store.LogsDir, logging.FileName))
	state, _ := store.LoadState(home)
	out := sinkSetupFailure{err: err != nil, pending: pending, dead: dead, requests: requests, reported: strings.Contains(string(log), "sink not built")}
	if state.LastFlush != nil {
		out.lastFlushOK = state.LastFlush.OK
		out.lastFlushError = state.LastFlush.Error
	}
	return out
}
