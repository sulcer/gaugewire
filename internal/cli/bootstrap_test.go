package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

type fakeDatabox struct {
	mu       sync.Mutex
	replies  map[string][]string // "METHOD /path" -> bodies in order (last repeats), status 200
	fail     map[string]int      // "METHOD /path" -> status for the error envelope
	calls    []string
	requests map[string][]string // "METHOD /path" -> request bodies in arrival order
	server   *httptest.Server
}

func newFakeDatabox(t *testing.T) *fakeDatabox {
	t.Helper()
	f := &fakeDatabox{replies: map[string][]string{}, fail: map[string]int{}, requests: map[string][]string{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		key := r.Method + " " + r.URL.Path
		f.calls = append(f.calls, key)
		f.requests[key] = append(f.requests[key], string(raw))
		w.Header().Set("Content-Type", "application/json")
		if status, ok := f.fail[key]; ok {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"requestId":"r","status":"error","errors":[{"code":"invalid_api_key","message":"bad","field":"","type":"auth"}]}`))
			return
		}
		queue := f.replies[key]
		if len(queue) == 0 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"requestId":"r","status":"error","errors":[{"code":"not_found","message":"no route","field":"","type":"routing"}]}`))
			return
		}
		body := queue[0]
		if len(queue) > 1 {
			f.replies[key] = queue[1:]
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeDatabox) on(key string, bodies ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[key] = append(f.replies[key], bodies...)
}

func (f *fakeDatabox) failWith(key string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[key] = status
}

func (f *fakeDatabox) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// bodies returns the request bodies sent to "METHOD /path", in arrival order.
func (f *fakeDatabox) bodies(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests[key]...)
}

const (
	oneAccount     = `{"requestId":"r","status":"success","accounts":[{"id":123456,"name":"Acme","accountType":"organization"}]}`
	twoAccounts    = `{"requestId":"r","status":"success","accounts":[{"id":123456,"name":"Acme","accountType":"organization"},{"id":7,"name":"Other","accountType":"client"}]}`
	noDataSources  = `{"requestId":"r","status":"success","dataSources":[]}`
	oneDataSource  = `{"requestId":"r","status":"success","dataSources":[{"id":4754489,"title":"Gaugewire","created":"2025-10-01T12:00:00.000000Z","timezone":"UTC","key":"ingestion","ingestionSupported":true}]}`
	createdSource  = `{"requestId":"r","status":"success","id":4754489,"title":"Gaugewire","created":"2025-10-01T12:00:00.000000Z","timezone":"UTC","key":"ingestion","ingestionSupported":true}`
	noDatasets     = `{"requestId":"r","status":"success","datasets":[]}`
	bothDatasets   = `{"requestId":"r","status":"success","datasets":[{"id":"ds-hist","title":"Claude Quota History","created":"x"},{"id":"ds-cur","title":"Claude Quota Current","created":"x"}]}`
	createdHistory = `{"requestId":"r","status":"success","id":"ds-hist","title":"Claude Quota History","created":"x"}`
	createdCurrent = `{"requestId":"r","status":"success","id":"ds-cur","title":"Claude Quota Current","created":"x"}`
	validKey       = `{"requestId":"r","status":"success"}`
)

func bootstrapOpts(f *fakeDatabox) bootstrapOptions {
	return bootstrapOptions{
		baseURL: f.server.URL, sinkID: "databox-main",
		getenv:     func(string) string { return "boot-key" },
		now:        func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) },
		httpClient: f.server.Client(),
	}
}

func freshHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	if err := config.Save(home, testConfig()); err != nil {
		t.Fatalf("save: %v", err)
	}
	return home
}

func TestBootstrapCreatesEverythingOnAnEmptyAccount(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/accounts", oneAccount)
	f.on("GET /v1/accounts/123456/data-sources", noDataSources)
	f.on("POST /v1/data-sources", createdSource)
	f.on("GET /v1/data-sources/4754489/datasets", noDatasets)
	f.on("POST /v1/datasets", createdHistory, createdCurrent)
	home := freshHome(t)
	var stdout bytes.Buffer
	err := bootstrap(t.Context(), home, bootstrapOpts(f), &stdout, &bytes.Buffer{})
	cfg, _ := config.Load(home)
	got := struct {
		err   bool
		out   string
		sinks []config.Sink
		calls []string
	}{err != nil, stdout.String(), cfg.Sinks, f.seen()}
	want := struct {
		err   bool
		out   string
		sinks []config.Sink
		calls []string
	}{
		out: strings.Join([]string{
			"account:          Acme (123456)",
			"data source:      Gaugewire (4754489) created",
			"history dataset:  ds-hist created",
			"current dataset:  ds-cur created",
		}, "\n") + "\n",
		sinks: []config.Sink{{
			ID: "databox-main", Type: "databox", Enabled: true, BaseURL: f.server.URL,
			AccountID: 123456, DataSourceID: 4754489, CurrentDatasetID: "ds-cur", HistoryDatasetID: "ds-hist",
			Credentials: config.Credentials{APIKeyEnv: config.DefaultAPIKeyEnv},
		}},
		calls: []string{
			"GET /v1/auth/validate-key", "GET /v1/accounts", "GET /v1/accounts/123456/data-sources",
			"POST /v1/data-sources", "GET /v1/data-sources/4754489/datasets", "POST /v1/datasets", "POST /v1/datasets",
		},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestBootstrapReusesExistingResources(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/accounts", oneAccount)
	f.on("GET /v1/accounts/123456/data-sources", oneDataSource)
	f.on("GET /v1/data-sources/4754489/datasets", bothDatasets)
	home := freshHome(t)
	var stdout bytes.Buffer
	err := bootstrap(t.Context(), home, bootstrapOpts(f), &stdout, &bytes.Buffer{})
	got := struct {
		err   bool
		out   string
		calls []string
	}{err != nil, stdout.String(), f.seen()}
	want := struct {
		err   bool
		out   string
		calls []string
	}{
		out: strings.Join([]string{
			"account:          Acme (123456)",
			"data source:      Gaugewire (4754489) reused",
			"history dataset:  ds-hist reused",
			"current dataset:  ds-cur reused",
		}, "\n") + "\n",
		calls: []string{
			"GET /v1/auth/validate-key", "GET /v1/accounts",
			"GET /v1/accounts/123456/data-sources", "GET /v1/data-sources/4754489/datasets",
		},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestBootstrapNeedsAccountIDWhenSeveralAccounts(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/accounts", twoAccounts)
	err := bootstrap(t.Context(), freshHome(t), bootstrapOpts(f), &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, ErrChooseAccount) {
		t.Fatalf("got %v, want ErrChooseAccount", err)
	}
}

func TestBootstrapUsesTheGivenAccountID(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/accounts", twoAccounts)
	f.on("GET /v1/accounts/7/data-sources", noDataSources)
	f.on("POST /v1/data-sources", createdSource)
	f.on("GET /v1/data-sources/4754489/datasets", bothDatasets)
	opts := bootstrapOpts(f)
	opts.accountID = 7
	home := freshHome(t)
	err := bootstrap(t.Context(), home, opts, &bytes.Buffer{}, &bytes.Buffer{})
	cfg, _ := config.Load(home)
	if err != nil || cfg.Sinks[0].AccountID != 7 {
		t.Fatalf("err=%v accountId=%d, want nil and 7", err, cfg.Sinks[0].AccountID)
	}
}

func TestBootstrapStopsOnAnInvalidKeyWithoutWriting(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.failWith("GET /v1/auth/validate-key", http.StatusUnauthorized)
	home := freshHome(t)
	err := bootstrap(t.Context(), home, bootstrapOpts(f), &bytes.Buffer{}, &bytes.Buffer{})
	cfg, _ := config.Load(home)
	if err == nil || len(cfg.Sinks) != 0 || len(f.seen()) != 1 {
		t.Fatalf("err=%v sinks=%d calls=%v, want an error, no sink saved, one call", err, len(cfg.Sinks), f.seen())
	}
}

func TestBootstrapRecordsTheKeyFilePath(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/accounts", oneAccount)
	f.on("GET /v1/accounts/123456/data-sources", oneDataSource)
	f.on("GET /v1/data-sources/4754489/datasets", bothDatasets)
	home := freshHome(t)
	keyFile := filepath.Join(t.TempDir(), "databox.key")
	if err := os.WriteFile(keyFile, []byte("file-key\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	opts := bootstrapOpts(f)
	opts.apiKeyFile = keyFile
	opts.getenv = func(string) string { return "" }
	err := bootstrap(t.Context(), home, opts, &bytes.Buffer{}, &bytes.Buffer{})
	cfg, _ := config.Load(home)
	if err != nil || cfg.Sinks[0].Credentials.APIKeyFile != keyFile || cfg.Sinks[0].Credentials.APIKeyEnv != "" {
		t.Fatalf("err=%v creds=%+v, want the key file recorded", err, cfg.Sinks[0].Credentials)
	}
}

// observedHome is a fresh home whose state holds both windows observed, as it
// is once Claude Code has shown its status line.
func observedHome(t *testing.T) string {
	t.Helper()
	home := freshHome(t)
	state := store.NewState()
	used := 24.0
	reset := time.Date(2026, 9, 19, 17, 0, 0, 0, time.UTC)
	state.Windows = quota.Windows{
		FiveHour: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used, ResetsAt: &reset},
		SevenDay: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used, ResetsAt: &reset},
	}
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	return home
}

// eventTypes lists the event_type of every record in the given ingestion bodies.
func eventTypes(t *testing.T, bodies []string) []string {
	t.Helper()
	var types []string
	for _, b := range bodies {
		var payload struct {
			Records []map[string]any `json:"records"`
		}
		if err := json.Unmarshal([]byte(b), &payload); err != nil {
			t.Fatalf("decode %q: %v", b, err)
		}
		for _, r := range payload.Records {
			types = append(types, fmt.Sprint(r["event_type"]))
		}
	}
	return types
}

const reusedResources = "account:          Acme (123456)\n" +
	"data source:      Gaugewire (4754489) reused\n" +
	"history dataset:  ds-hist reused\n" +
	"current dataset:  ds-cur reused\n"

func TestBootstrapTestIngestSendsOneHeartbeat(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/accounts", oneAccount)
	f.on("GET /v1/accounts/123456/data-sources", oneDataSource)
	f.on("GET /v1/data-sources/4754489/datasets", bothDatasets)
	f.on("POST /v1/datasets/ds-hist/data", `{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`)
	f.on("POST /v1/datasets/ds-cur/data", `{"requestId":"r","status":"success","ingestionId":"ing-c","message":"ok"}`)
	opts := bootstrapOpts(f)
	opts.testIngest = true
	var stdout bytes.Buffer
	err := bootstrap(t.Context(), observedHome(t), opts, &stdout, &bytes.Buffer{})
	got := struct {
		err     bool
		out     string
		history []string
	}{err != nil, stdout.String(), eventTypes(t, f.bodies("POST /v1/datasets/ds-hist/data"))}
	want := struct {
		err     bool
		out     string
		history []string
	}{false, reusedResources + "test ingest:      history ing-h, current ing-c\n", []string{"heartbeat"}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestBootstrapTestIngestSkipsWithoutAnObservation(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/accounts", oneAccount)
	f.on("GET /v1/accounts/123456/data-sources", oneDataSource)
	f.on("GET /v1/data-sources/4754489/datasets", bothDatasets)
	opts := bootstrapOpts(f)
	opts.testIngest = true
	var stdout bytes.Buffer
	err := bootstrap(t.Context(), freshHome(t), opts, &stdout, &bytes.Buffer{})
	got := struct {
		err   bool
		out   string
		calls []string
	}{err != nil, stdout.String(), f.seen()}
	want := struct {
		err   bool
		out   string
		calls []string
	}{
		out: reusedResources + "test ingest:      skipped: no quota observation yet; run it again after Claude Code has shown its status line\n",
		calls: []string{
			"GET /v1/auth/validate-key", "GET /v1/accounts",
			"GET /v1/accounts/123456/data-sources", "GET /v1/data-sources/4754489/datasets",
		},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunDataboxRejectsMissingOrUnknownSubcommand(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "missing", args: nil},
		{name: "unknown", args: []string{"nope"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			err := runDatabox(t.Context(), tc.args, BuildInfo{}, IO{Stdout: &stdout, Stderr: &stderr})
			got := struct {
				usage  bool
				stdout string
			}{errors.Is(err, ErrUsage), stdout.String()}
			if diff := cmp.Diff(struct {
				usage  bool
				stdout string
			}{usage: true}, got, cmp.AllowUnexported(got)); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
