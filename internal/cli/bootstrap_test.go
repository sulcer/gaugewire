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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/sink/databox"
	"github.com/sulcer/gaugewire/internal/store"
)

type fakeDatabox struct {
	mu       sync.Mutex
	replies  map[string][]string // "METHOD /path" -> bodies in order (last repeats), status 200
	fail     map[string]int      // "METHOD /path" -> status for the error envelope
	calls    []string
	accounts []string            // x-account-id of each call, in arrival order
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
		f.accounts = append(f.accounts, r.Header.Get("x-account-id"))
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

func (f *fakeDatabox) accountHeaders() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.accounts...)
}

func (f *fakeDatabox) bodies(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests[key]...)
}

const (
	organization   = `{"requestId":"r","status":"success","data":{"id":123456,"name":"Acme","companyName":"Acme, Inc."}}`
	noDataSources  = `{"requestId":"r","status":"success","data":{"items":[],"pagination":{"page":0,"pageSize":100,"totalItems":0}}}`
	oneDataSource  = `{"requestId":"r","status":"success","data":{"items":[{"id":4754489,"name":"Gaugewire","integrationKey":"DataboxAPI","timezone":"UTC"}],"pagination":{"page":0,"pageSize":100,"totalItems":1}}}`
	createdSource  = `{"requestId":"r","status":"success","data":{"id":4754489,"name":"Gaugewire","integrationKey":"DataboxAPI","timezone":"UTC"}}`
	noDatasets     = `{"requestId":"r","status":"success","data":{"items":[],"pagination":{"page":0,"pageSize":100,"totalItems":0}}}`
	bothDatasets   = `{"requestId":"r","status":"success","data":{"items":[{"id":901,"dataSourceId":4754489,"name":"Claude Quota History"},{"id":902,"dataSourceId":4754489,"name":"Claude Quota Current"}],"pagination":{"page":0,"pageSize":100,"totalItems":2}}}`
	createdHistory = `{"requestId":"r","status":"success","data":{"id":901,"dataSourceId":4754489,"name":"Claude Quota History"}}`
	createdCurrent = `{"requestId":"r","status":"success","data":{"id":902,"dataSourceId":4754489,"name":"Claude Quota Current"}}`
	emptyData      = `{"requestId":"r","status":"success","data":{}}`
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
	f.on("GET /v2/auth/validate-key", validKey)
	f.on("GET /v2/organization", organization)
	f.on("GET /v2/data-sources", noDataSources)
	f.on("POST /v2/data-sources", createdSource)
	f.on("GET /v2/datasets", noDatasets)
	f.on("POST /v2/datasets", createdHistory, createdCurrent)
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
			"organization:     Acme (123456)",
			"data source:      Gaugewire (4754489) created",
			"history dataset:  901 created",
			"current dataset:  902 created",
		}, "\n") + "\n",
		sinks: []config.Sink{{
			ID: "databox-main", Type: "databox", Enabled: true, BaseURL: f.server.URL,
			DataSourceID: 4754489, CurrentDatasetID: 902, HistoryDatasetID: 901,
			Credentials: config.Credentials{APIKeyEnv: config.DefaultAPIKeyEnv},
		}},
		calls: []string{
			"GET /v2/auth/validate-key", "GET /v2/organization", "GET /v2/data-sources",
			"POST /v2/data-sources", "GET /v2/datasets", "POST /v2/datasets", "POST /v2/datasets",
		},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestBootstrapReusesExistingResources(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v2/auth/validate-key", validKey)
	f.on("GET /v2/organization", organization)
	f.on("GET /v2/data-sources", oneDataSource)
	f.on("GET /v2/datasets", bothDatasets)
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
			"organization:     Acme (123456)",
			"data source:      Gaugewire (4754489) reused",
			"history dataset:  901 reused",
			"current dataset:  902 reused",
		}, "\n") + "\n",
		calls: []string{
			"GET /v2/auth/validate-key", "GET /v2/organization",
			"GET /v2/data-sources", "GET /v2/datasets",
		},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestBootstrapActsOnTheGivenAccount(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v2/auth/validate-key", validKey)
	f.on("GET /v2/organization", `{"requestId":"r","status":"success","data":{"id":7,"name":"Client A"}}`)
	f.on("GET /v2/data-sources", noDataSources)
	f.on("POST /v2/data-sources", createdSource)
	f.on("GET /v2/datasets", bothDatasets)
	opts := bootstrapOpts(f)
	opts.accountID = 7
	home := freshHome(t)
	err := bootstrap(t.Context(), home, opts, &bytes.Buffer{}, &bytes.Buffer{})
	cfg, _ := config.Load(home)
	got := struct {
		err      bool
		sinks    []config.Sink
		accounts []string
	}{err != nil, cfg.Sinks, f.accountHeaders()}
	want := struct {
		err      bool
		sinks    []config.Sink
		accounts []string
	}{false, []config.Sink{{
		ID: "databox-main", Type: "databox", Enabled: true, BaseURL: f.server.URL,
		AccountID: 7, DataSourceID: 4754489, CurrentDatasetID: 902, HistoryDatasetID: 901,
		Credentials: config.Credentials{APIKeyEnv: config.DefaultAPIKeyEnv},
	}}, []string{"7", "7", "7", "7", "7"}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestBootstrapCreatesDatasetsWithTheirSchemas(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v2/auth/validate-key", validKey)
	f.on("GET /v2/organization", organization)
	f.on("GET /v2/data-sources", oneDataSource)
	f.on("GET /v2/datasets", noDatasets)
	f.on("POST /v2/datasets", createdHistory, createdCurrent)
	err := bootstrap(t.Context(), freshHome(t), bootstrapOpts(f), &bytes.Buffer{}, &bytes.Buffer{})
	type request struct {
		DataSourceID int64            `json:"dataSourceId"`
		Name         string           `json:"name"`
		PrimaryKey   []string         `json:"primaryKey"`
		Schema       []databox.Column `json:"schema"`
	}
	requests := []request{}
	for _, b := range f.bodies("POST /v2/datasets") {
		var r request
		if decodeErr := json.Unmarshal([]byte(b), &r); decodeErr != nil {
			t.Fatalf("decode %q: %v", b, decodeErr)
		}
		requests = append(requests, r)
	}
	got := struct {
		err      bool
		requests []request
	}{err != nil, requests}
	want := struct {
		err      bool
		requests []request
	}{false, []request{
		{4754489, "Claude Quota History", []string{"event_id"}, databox.HistorySchema()},
		{4754489, "Claude Quota Current", []string{"account_id"}, databox.CurrentSchema()},
	}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestBootstrapStopsOnAnInvalidKeyWithoutWriting(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.failWith("GET /v2/auth/validate-key", http.StatusUnauthorized)
	home := freshHome(t)
	err := bootstrap(t.Context(), home, bootstrapOpts(f), &bytes.Buffer{}, &bytes.Buffer{})
	cfg, _ := config.Load(home)
	got := struct {
		err   bool
		sinks int
		calls []string
	}{err != nil, len(cfg.Sinks), f.seen()}
	want := struct {
		err   bool
		sinks int
		calls []string
	}{true, 0, []string{"GET /v2/auth/validate-key"}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestBootstrapRecordsTheKeyFilePath(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v2/auth/validate-key", validKey)
	f.on("GET /v2/organization", organization)
	f.on("GET /v2/data-sources", oneDataSource)
	f.on("GET /v2/datasets", bothDatasets)
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
	type outcome struct {
		err   bool
		creds config.Credentials
	}
	var got outcome
	got.err = err != nil
	if len(cfg.Sinks) == 1 {
		got.creds = cfg.Sinks[0].Credentials
	}
	if want := (outcome{false, config.Credentials{APIKeyFile: keyFile}}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// observedHome holds both windows observed, as after Claude Code's status line.
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

const reusedResources = "organization:     Acme (123456)\n" +
	"data source:      Gaugewire (4754489) reused\n" +
	"history dataset:  901 reused\n" +
	"current dataset:  902 reused\n"

func TestBootstrapTestIngestSendsOneHeartbeat(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v2/auth/validate-key", validKey)
	f.on("GET /v2/organization", organization)
	f.on("GET /v2/data-sources", oneDataSource)
	f.on("GET /v2/datasets", bothDatasets)
	f.on("POST /v2/datasets/901/data", `{"requestId":"r","status":"success","data":{"ingestionId":"ing-h","status":"pending","message":"ok"}}`)
	f.on("POST /v2/datasets/902/data", `{"requestId":"r","status":"success","data":{"ingestionId":"ing-c","status":"pending","message":"ok"}}`)
	opts := bootstrapOpts(f)
	opts.testIngest = true
	var stdout bytes.Buffer
	err := bootstrap(t.Context(), observedHome(t), opts, &stdout, &bytes.Buffer{})
	got := struct {
		err     bool
		out     string
		history []string
	}{err != nil, stdout.String(), eventTypes(t, f.bodies("POST /v2/datasets/901/data"))}
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
	f.on("GET /v2/auth/validate-key", validKey)
	f.on("GET /v2/organization", organization)
	f.on("GET /v2/data-sources", oneDataSource)
	f.on("GET /v2/datasets", bothDatasets)
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
			"GET /v2/auth/validate-key", "GET /v2/organization",
			"GET /v2/data-sources", "GET /v2/datasets",
		},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

// The read-only home lets state.json load but fails the sink's save, so the
// read-back finds the record an earlier run left.
func TestBootstrapTestIngestRefusesARecordLeftByAnEarlierRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes are not meaningful on Windows")
	}
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("POST /v2/datasets/901/data", `{"requestId":"r","status":"success","data":{"ingestionId":"ing-h","status":"pending","message":"ok"}}`)
	f.on("POST /v2/datasets/902/data", `{"requestId":"r","status":"success","data":{"ingestionId":"ing-c","status":"pending","message":"ok"}}`)
	home := observedHome(t)
	state, err := store.LoadState(home)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	state.LastIngestion = map[string]store.IngestionRecord{
		"databox-main": {Current: "ing-c-old", History: "ing-h-old", At: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)},
	}
	if saveErr := store.SaveState(home, state); saveErr != nil {
		t.Fatalf("save state: %v", saveErr)
	}
	if lockErr := os.WriteFile(filepath.Join(home, store.StateLockFile), nil, 0o600); lockErr != nil {
		t.Fatalf("lock file: %v", lockErr)
	}
	if chmodErr := os.Chmod(home, 0o500); chmodErr != nil {
		t.Fatalf("chmod: %v", chmodErr)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	if os.WriteFile(filepath.Join(home, "probe"), nil, 0o600) == nil {
		t.Skip("directory modes do not stop this user from writing")
	}
	client, err := databox.NewClient(f.server.URL, "boot-key", 0, f.server.Client())
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	entry := config.Sink{ID: "databox-main", Type: config.SinkTypeDatabox, Enabled: true, HistoryDatasetID: 901, CurrentDatasetID: 902}
	var stdout bytes.Buffer
	err = sendTestHeartbeat(t.Context(), home, testConfig(), entry, client, bootstrapOpts(f), &stdout)
	got := struct {
		err   string
		out   string
		calls []string
	}{fmt.Sprint(err), stdout.String(), f.seen()}
	want := struct {
		err   string
		out   string
		calls []string
	}{
		err:   "test ingest was accepted but the ingestion record could not be read back; check logs/gaugewire.log",
		calls: []string{"POST /v2/datasets/901/data", "POST /v2/datasets/902/data"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

// homeWithRecord holds a sink whose dataset ids are history and current and
// the ingestion record a flush to them left.
func homeWithRecord(t *testing.T, history, current config.DatasetID) string {
	t.Helper()
	home := observedHome(t)
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Sinks = []config.Sink{{
		ID: "databox-main", Type: config.SinkTypeDatabox, Enabled: true,
		DataSourceID: 4754489, HistoryDatasetID: history, CurrentDatasetID: current,
		Credentials: config.Credentials{APIKeyEnv: config.DefaultAPIKeyEnv},
	}}
	if saveErr := config.Save(home, cfg); saveErr != nil {
		t.Fatalf("save: %v", saveErr)
	}
	state, err := store.LoadState(home)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	state.LastIngestion = map[string]store.IngestionRecord{"databox-main": oldRecord}
	if saveErr := store.SaveState(home, state); saveErr != nil {
		t.Fatalf("save state: %v", saveErr)
	}
	return home
}

var oldRecord = store.IngestionRecord{Current: "ing-c-old", History: "ing-h-old", At: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)}

func TestBootstrapResetsTheIngestionRecordWhenTheDatasetsChange(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		history, current config.DatasetID
		want             store.IngestionRecord
	}{
		{name: "upgraded from v1", history: 0, current: 0, want: store.IngestionRecord{}},
		{name: "same datasets", history: 901, current: 902, want: oldRecord},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeDatabox(t)
			f.on("GET /v2/auth/validate-key", validKey)
			f.on("GET /v2/organization", organization)
			f.on("GET /v2/data-sources", oneDataSource)
			f.on("GET /v2/datasets", bothDatasets)
			home := homeWithRecord(t, tc.history, tc.current)
			err := bootstrap(t.Context(), home, bootstrapOpts(f), &bytes.Buffer{}, &bytes.Buffer{})
			state, _ := store.LoadState(home)
			got := struct {
				err bool
				rec store.IngestionRecord
			}{err != nil, state.LastIngestion["databox-main"]}
			want := struct {
				err bool
				rec store.IngestionRecord
			}{false, tc.want}
			if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBootstrapRefusesACreatedResourceWithoutAnID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		script  map[string]string
		wantErr string
	}{
		{
			name:    "data source",
			script:  map[string]string{"GET /v2/data-sources": noDataSources, "POST /v2/data-sources": emptyData},
			wantErr: "databox returned no id for the created data source",
		},
		{
			name: "dataset",
			script: map[string]string{
				"GET /v2/data-sources": oneDataSource,
				"GET /v2/datasets":     noDatasets,
				"POST /v2/datasets":    emptyData,
			},
			wantErr: `databox returned no id for the created dataset "Claude Quota History"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFakeDatabox(t)
			f.on("GET /v2/auth/validate-key", validKey)
			f.on("GET /v2/organization", organization)
			for key, body := range tc.script {
				f.on(key, body)
			}
			home := freshHome(t)
			err := bootstrap(t.Context(), home, bootstrapOpts(f), &bytes.Buffer{}, &bytes.Buffer{})
			cfg, _ := config.Load(home)
			got := struct {
				err   string
				sinks int
			}{fmt.Sprint(err), len(cfg.Sinks)}
			want := struct {
				err   string
				sinks int
			}{tc.wantErr, 0}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
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
