# Databox Sink Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver spooled quota events to Databox: a thin REST API v1 client, the two dataset record shapes, a `sink.Sink` that writes History then Current with the `currentCapturedAt` guard and remembers ingestion ids, `gaugewire databox bootstrap` that creates or reuses the data source and datasets idempotently, and the three network rows of `doctor`. Every test talks to `httptest`; no test contacts api.databox.com.

**Architecture:** `internal/sink/databox` holds the client, error classification, record mapping and the sink; it imports only `sink` and `quota`. The `sink` package gains a tiny `IngestionStore` (read and write the per-sink ingestion record under `state.lock`) so the Databox sink never touches `store`. `internal/cli` loads the API key, wires `buildSinks`, adds the bootstrap subcommand and the doctor rows.

**Tech Stack:** Go 1.27 standard library (`net/http`, `net/http/httptest`, `encoding/json`, `flag`), existing deps only.

**Spec:** `docs/spec/gaugewire/databox-sink.md` (with the corrections in Task 7), `spool-and-flush.md` (delivery, `lastIngestion`), `data-contract.md` (`lastIngestion`, sink config), `cli-and-install.md` (Commands, doctor rows, Logging), `architecture.md`, `testing-strategy.md`, `docs/how-tos/acceptance-test.md`. Public documentation the plan relies on (https://developers.databox.com, API v1 pages fetched 2026-09-19): base `https://api.databox.com`, header `x-api-key`; `GET /v1/auth/validate-key` (200 valid, 401 missing or invalid); `GET /v1/accounts` → `{requestId, status, accounts:[{id int, name, accountType}]}`; `GET /v1/accounts/{accountId}/data-sources` → `{dataSources:[{id int, title, created, timezone, key, ingestionSupported}]}`; `POST /v1/data-sources` body `{title (required), accountId int, timezone}` → `{id int, title, created, timezone, key, ingestionSupported}`; `GET /v1/data-sources/{id}/datasets` → `{datasets:[{id string, title, created}]}`; `POST /v1/datasets` body `{title, dataSourceId int (required), primaryKeys []string|null}` → `{id string, title, created}` (no column schema field is documented); `POST /v1/datasets/{id}/data` body `{records:[{...}]}`, at most 100 records per ingestion event → `{requestId, status, ingestionId, message}`; `GET /v1/datasets/{id}/ingestions/{ingestionId}` → `{requestId, status, ingestionId, timestamp, metrics{datasetMetrics{columnsCount, datasetSizeMB, totalDatasetRecordsCount}, ingestionMetrics{appendedRecordsCount, receivedRecordsCount, rejectedRecordsCount, overwrittenRecordsCount}}}`; error envelope `{requestId, status:"error", errors:[{code (string|null), message, field, type}]}` on 400 and 401; rate limits 10 000 requests per hour and 10 per second per key, payloads at most 500 rows or 10 MB per request. Not documented: status codes for other errors, the body of a 429, whether column types are inferred from the first ingestion (recorded as assumptions to verify).

## Global Constraints

- Branch `feat/databox-sink` from `main` after PR #4 merges (else from the head of `feat/install-and-doctor`). Conventional Commits without scope, first line ≤ 72 characters, `Co-Authored-By: Claude <model> <noreply@anthropic.com>` trailer via the heredoc form. `make check` before every commit; git hooks are active and never bypassed.
- **Commit authority:** commits on this branch are pre-approved for this plan's execution; never push.
- No new dependencies. Dependency direction: `sink/databox → sink, quota`; `sink → store, quota`; `cli → everything`; nothing imports `cli`.
- **No test contacts a real service.** Every client, sink, bootstrap and doctor test uses `httptest.NewServer`; a sink config with an empty `baseUrl` is refused by the constructor so a test cannot fall through to the real host.
- **The API key never appears in a log line, an error string, config.json, state.json, an event file or a test golden.** Errors carry the HTTP status, the envelope code and message, and the `requestId`; never the request headers. One test asserts the key is absent from the log after a failed delivery.
- Each request has a 15 s timeout (`sink.RequestTimeout`); the flusher's 2 min run cap still applies.
- Tests: `t.Parallel()` unless `t.Setenv`; one whole-value assertion per test (a composite assertion on one outcome value counts as one); expectations from this plan, the spec and fixtures, never from running the code. Tests that spawn the built binary carry `//go:build integration`.
- Files are created with the Write tool; Go files are gofumpt-formatted by the edit hook.
- Third-party behaviour is relied on only when its public documentation states it; the two open assumptions stay listed in the spec until the acceptance test answers them. Nothing in the repo may mention internal services, repositories, code or tooling of any third party; grep `git grep -n -i -E 'internal databox|databox services|horizon|environments-api|ingestion-api|~/databox|marketplace' -- ':!docs/research'` before every commit and expect no output.
- Spec markers for the sink stay `Partial` until the acceptance test on a real machine has run; this plan builds the code, not the proof.

## Before you start

```bash
git switch main && git pull --ff-only
git switch -c feat/databox-sink
```

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/sink/databox/client.go`, `errors.go`, `client_test.go`, `errors_test.go`, `fake_test.go` | REST v1 client, error classification, scripted fake server for tests |
| `internal/sink/databox/records.go`, `records_test.go`, `testdata/*.golden` | History and Current record shapes |
| `internal/sink/ingestion.go`, `ingestion_test.go` | `Ingestion`, `IngestionStore`, `StateIngestions` (under `state.lock`) |
| `internal/sink/databox/sink.go`, `sink_test.go` | the `sink.Sink` implementation |
| `internal/cli/apikey.go`, `apikey_test.go`, `sinks.go` (modify), `flush_databox_test.go` | key loading, sink construction, end-to-end flush against `httptest` |
| `internal/cli/bootstrap.go`, `bootstrap_test.go`, `cli.go` (modify) | `gaugewire databox bootstrap` |
| `internal/cli/doctor.go`, `doctor_checks.go`, `doctor_test.go`, `testdata/doctor_sink.golden` (modify/create) | the three sink rows |
| `cmd/gaugewire/main_integration_test.go` (modify) | `flush` through the built binary against `httptest` |
| `docs/spec/gaugewire/databox-sink.md`, `cli-and-install.md`, `data-contract.md`, `spool-and-flush.md`, `testing-strategy.md`, `docs/how-tos/acceptance-test.md`, `docs/spec/README.md` | facts corrected, markers, commands |

---

### Task 1: Client and error classification

**Files:**
- Create: `internal/sink/databox/client.go`, `internal/sink/databox/errors.go`, `internal/sink/databox/client_test.go`, `internal/sink/databox/errors_test.go`, `internal/sink/databox/fake_test.go`

**Interfaces:**
- Consumes: `sink.NewRetryable`, `sink.NewPermanent`, `sink.RequestTimeout`.
- Produces: `databox.DefaultBaseURL` ("https://api.databox.com"); `databox.Client` with `NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error)` (empty base URL or key → error; `httpClient` nil → `&http.Client{}`); methods `ValidateKey(ctx) error`, `Accounts(ctx) ([]Account, error)`, `DataSources(ctx, accountID int64) ([]DataSource, error)`, `CreateDataSource(ctx, accountID int64, title, timezone string) (DataSource, error)`, `Datasets(ctx, dataSourceID int64) ([]Dataset, error)`, `CreateDataset(ctx, dataSourceID int64, title string, primaryKeys []string) (Dataset, error)`, `Ingest(ctx, datasetID string, records []map[string]any) (string, error)` (returns `ingestionId`), `Ingestion(ctx, datasetID, ingestionID string) (Ingestion, error)`; types `Account{ID int64; Name, AccountType string}`, `DataSource{ID int64; Title, Timezone string; IngestionSupported bool}`, `Dataset{ID, Title string}`, `Ingestion{ID, Status, Timestamp string; Metrics IngestionMetrics}`, `IngestionMetrics{Appended, Received, Rejected, Overwritten int}`; `databox.MaxRecords` (100); unexported `classify(status int, body []byte) error` returning a `*sink.Error`.

Classification (status first, then the envelope's first `code`, default codes in parentheses): 401 → permanent (`invalid_api_key`); 403 → permanent (`forbidden`); 400, 404, 413, 422 → permanent (`invalid_request`); 408 → retryable (`timeout`); 429 → retryable (`rate_limited`); 5xx → retryable (`server_error`); any other 4xx → permanent (`invalid_request`); transport error or context deadline → retryable (`transport`); a 2xx whose body does not decode → retryable (`invalid_response`). Message format: `databox: HTTP <status> <code>: <message> (request <requestId>)`, message and requestId omitted when absent. Every request sets `x-api-key`, `Content-Type: application/json` on POST, `Accept: application/json`, and a per-call `context.WithTimeout(ctx, sink.RequestTimeout)`. Bodies are read with `io.LimitReader(resp.Body, 1<<20)`.

- [ ] **Step 1: Write the fake server and the failing tests**

`internal/sink/databox/fake_test.go`:

```go
package databox

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// call is one request the fake saw.
type call struct {
	Method string
	Path   string
	Key    string
	Body   string
}

// reply is one scripted response for a method+path.
type reply struct {
	Status int
	Body   string
}

// fake is a scripted Databox API: replies are keyed by "METHOD /path"; an
// unscripted request gets 404 with an error envelope. It records every call.
type fake struct {
	t       *testing.T
	mu      sync.Mutex
	replies map[string][]reply
	calls   []call
	server  *httptest.Server
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{t: t, replies: map[string][]reply{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// on queues a reply for a method and path; several replies are served in order
// and the last one repeats.
func (f *fake) on(method, path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := method + " " + path
	f.replies[key] = append(f.replies[key], reply{Status: status, Body: body})
}

func (f *fake) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{Method: r.Method, Path: r.URL.Path, Key: r.Header.Get("x-api-key"), Body: string(raw)})
	key := r.Method + " " + r.URL.Path
	queue := f.replies[key]
	if len(queue) == 0 {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"requestId":"req-404","status":"error","errors":[{"code":"not_found","message":"no route","field":"","type":"routing"}]}`))
		return
	}
	rep := queue[0]
	if len(queue) > 1 {
		f.replies[key] = queue[1:]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rep.Status)
	_, _ = w.Write([]byte(rep.Body))
}

func (f *fake) seen() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
```

`internal/sink/databox/client_test.go`:

```go
package databox

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/sink"
)

const testKey = "dbx-test-key-0123"

func client(t *testing.T, f *fake) *Client {
	t.Helper()
	c, err := NewClient(f.server.URL, testKey, f.server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestNewClientRefusesAnEmptyBaseURLOrKey(t *testing.T) {
	t.Parallel()
	_, errURL := NewClient("", "k", nil)
	_, errKey := NewClient("https://example.test", "", nil)
	if errURL == nil || errKey == nil {
		t.Fatalf("errURL=%v errKey=%v, want both non-nil", errURL, errKey)
	}
}

func TestValidateKeySendsTheHeader(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v1/auth/validate-key", 200, `{"requestId":"r1","status":"success"}`)
	err := client(t, f).ValidateKey(t.Context())
	got := struct {
		err   bool
		calls []call
	}{err != nil, f.seen()}
	want := struct {
		err   bool
		calls []call
	}{false, []call{{Method: "GET", Path: "/v1/auth/validate-key", Key: testKey}}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestAccountsDecodesTheList(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v1/accounts", 200, `{"requestId":"r","status":"success","accounts":[{"id":123456,"name":"Acme","accountType":"organization"}]}`)
	got, err := client(t, f).Accounts(t.Context())
	want := []Account{{ID: 123456, Name: "Acme", AccountType: "organization"}}
	if err != nil || !cmp.Equal(want, got) {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

func TestDataSourcesAndCreate(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v1/accounts/123456/data-sources", 200, `{"requestId":"r","status":"success","dataSources":[{"id":4754489,"title":"Gaugewire","created":"2025-10-01T12:00:00.000000Z","timezone":"UTC","key":"ingestion","ingestionSupported":true}]}`)
	f.on("POST", "/v1/data-sources", 200, `{"requestId":"r","status":"success","id":4754490,"title":"Gaugewire","created":"2025-10-01T12:00:00.000000Z","timezone":"UTC","key":"ingestion","ingestionSupported":true}`)
	c := client(t, f)
	listed, errList := c.DataSources(t.Context(), 123456)
	created, errCreate := c.CreateDataSource(t.Context(), 123456, "Gaugewire", "UTC")
	type outcome struct {
		listed  []DataSource
		created DataSource
		errs    [2]bool
		body    string
	}
	got := outcome{listed, created, [2]bool{errList != nil, errCreate != nil}, f.seen()[1].Body}
	want := outcome{
		listed:  []DataSource{{ID: 4754489, Title: "Gaugewire", Timezone: "UTC", IngestionSupported: true}},
		created: DataSource{ID: 4754490, Title: "Gaugewire", Timezone: "UTC", IngestionSupported: true},
		body:    `{"accountId":123456,"timezone":"UTC","title":"Gaugewire"}`,
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasetsAndCreate(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v1/data-sources/4754489/datasets", 200, `{"requestId":"r","status":"success","datasets":[{"id":"ds-hist","title":"Claude Quota History","created":"2025-10-01T12:00:00.000000Z"}]}`)
	f.on("POST", "/v1/datasets", 200, `{"requestId":"r","status":"success","id":"ds-cur","title":"Claude Quota Current","created":"2025-10-01T12:00:00.000000Z"}`)
	c := client(t, f)
	listed, errList := c.Datasets(t.Context(), 4754489)
	created, errCreate := c.CreateDataset(t.Context(), 4754489, "Claude Quota Current", []string{"account_id"})
	type outcome struct {
		listed  []Dataset
		created Dataset
		errs    [2]bool
		body    string
	}
	got := outcome{listed, created, [2]bool{errList != nil, errCreate != nil}, f.seen()[1].Body}
	want := outcome{
		listed:  []Dataset{{ID: "ds-hist", Title: "Claude Quota History"}},
		created: Dataset{ID: "ds-cur", Title: "Claude Quota Current"},
		body:    `{"dataSourceId":4754489,"primaryKeys":["account_id"],"title":"Claude Quota Current"}`,
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestIngestReturnsTheIngestionID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v1/datasets/ds-hist/data", 200, `{"requestId":"r","status":"success","ingestionId":"ing-1","message":"accepted"}`)
	id, err := client(t, f).Ingest(t.Context(), "ds-hist", []map[string]any{{"event_id": "evt-1", "five_hour_used_percentage": 24.0}})
	got := struct {
		id   string
		err  bool
		body string
	}{id, err != nil, f.seen()[0].Body}
	want := struct {
		id   string
		err  bool
		body string
	}{"ing-1", false, `{"records":[{"event_id":"evt-1","five_hour_used_percentage":24}]}`}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestIngestRefusesMoreThanMaxRecords(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	records := make([]map[string]any, MaxRecords+1)
	_, err := client(t, f).Ingest(t.Context(), "ds-hist", records)
	class, code := sink.Classify(err)
	if err == nil || class != sink.Permanent || code != "too_many_records" || len(f.seen()) != 0 {
		t.Fatalf("err=%v class=%v code=%q calls=%d; want a permanent too_many_records error and no request", err, class, code, len(f.seen()))
	}
}

func TestIngestionDecodesMetrics(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v1/datasets/ds-hist/ingestions/ing-1", 200, `{"requestId":"r","status":"success","ingestionId":"ing-1","timestamp":"2026-09-19T10:00:00.000000Z","metrics":{"datasetMetrics":{"columnsCount":18,"datasetSizeMB":0.1,"totalDatasetRecordsCount":40},"ingestionMetrics":{"appendedRecordsCount":2,"receivedRecordsCount":3,"rejectedRecordsCount":0,"overwrittenRecordsCount":1}}}`)
	got, err := client(t, f).Ingestion(t.Context(), "ds-hist", "ing-1")
	want := Ingestion{ID: "ing-1", Status: "success", Timestamp: "2026-09-19T10:00:00.000000Z", Metrics: IngestionMetrics{Appended: 2, Received: 3, Rejected: 0, Overwritten: 1}}
	if err != nil || got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

func TestErrorsNeverCarryTheKey(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v1/auth/validate-key", 401, `{"requestId":"req-9","status":"error","errors":[{"code":"invalid_api_key","message":"key rejected","field":"","type":"auth"}]}`)
	err := client(t, f).ValidateKey(t.Context())
	class, code := sink.Classify(err)
	got := struct {
		class   sink.Class
		code    string
		message string
	}{class, code, err.Error()}
	want := struct {
		class   sink.Class
		code    string
		message string
	}{sink.Permanent, "invalid_api_key", "permanent (invalid_api_key): databox: HTTP 401 invalid_api_key: key rejected (request req-9)"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestTransportFailureIsRetryable(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	url := f.server.URL
	f.server.Close()
	c, err := NewClient(url, testKey, &http.Client{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.ValidateKey(context.Background())
	class, code := sink.Classify(err)
	if err == nil || class != sink.Retryable || code != "transport" {
		t.Fatalf("err=%v class=%v code=%q, want retryable transport", err, class, code)
	}
}

func TestUndecodableSuccessBodyIsRetryable(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v1/accounts", 200, `<html>`)
	_, err := client(t, f).Accounts(t.Context())
	class, code := sink.Classify(err)
	if err == nil || class != sink.Retryable || code != "invalid_response" || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v class=%v code=%q, want retryable invalid_response", err, class, code)
	}
}
```

`internal/sink/databox/errors_test.go`:

```go
package databox

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/sink"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	envelope := `{"requestId":"req-1","status":"error","errors":[{"code":"schema_mismatch","message":"column x","field":"x","type":"validation"}]}`
	type classified struct {
		class sink.Class
		code  string
	}
	cases := map[string]struct {
		status int
		body   string
		want   classified
	}{
		"401 with envelope":      {401, `{"errors":[{"code":"invalid_api_key","message":"bad"}]}`, classified{sink.Permanent, "invalid_api_key"}},
		"401 without body":       {401, ``, classified{sink.Permanent, "invalid_api_key"}},
		"403":                    {403, ``, classified{sink.Permanent, "forbidden"}},
		"400 with envelope code": {400, envelope, classified{sink.Permanent, "schema_mismatch"}},
		"404 default code":       {404, ``, classified{sink.Permanent, "invalid_request"}},
		"413":                    {413, ``, classified{sink.Permanent, "invalid_request"}},
		"422":                    {422, ``, classified{sink.Permanent, "invalid_request"}},
		"408":                    {408, ``, classified{sink.Retryable, "timeout"}},
		"429 with envelope":      {429, `{"errors":[{"code":"rate_limited","message":"slow down"}]}`, classified{sink.Retryable, "rate_limited"}},
		"429 with html body":     {429, `<html>`, classified{sink.Retryable, "rate_limited"}},
		"500":                    {500, ``, classified{sink.Retryable, "server_error"}},
		"503 with envelope":      {503, `{"errors":[{"code":"service_unavailable","message":"later"}]}`, classified{sink.Retryable, "service_unavailable"}},
		"418 other 4xx":          {418, ``, classified{sink.Permanent, "invalid_request"}},
	}
	got := map[string]classified{}
	want := map[string]classified{}
	for name, tc := range cases {
		class, code := sink.Classify(classify(tc.status, []byte(tc.body)))
		got[name] = classified{class, code}
		want[name] = tc.want
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(classified{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/sink/databox/`
Expected: FAIL to build, `undefined: NewClient`, `undefined: Client`, `undefined: Account`, `undefined: classify`, `undefined: MaxRecords`.

- [ ] **Step 3: Write the implementation**

`internal/sink/databox/errors.go`:

```go
package databox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/sulcer/gaugewire/internal/sink"
)

// envelope is the documented error body.
type envelope struct {
	RequestID string `json:"requestId"`
	Errors    []struct {
		Code    *string `json:"code"`
		Message string  `json:"message"`
	} `json:"errors"`
}

// classify turns an error response into a retryable or permanent sink error.
// The HTTP status decides the class; the envelope's first code names it.
func classify(status int, body []byte) error {
	var env envelope
	_ = json.Unmarshal(body, &env)
	code, message := "", ""
	if len(env.Errors) > 0 {
		if env.Errors[0].Code != nil {
			code = *env.Errors[0].Code
		}
		message = env.Errors[0].Message
	}
	class, fallback := classOf(status)
	if code == "" {
		code = fallback
	}
	text := fmt.Sprintf("databox: HTTP %d %s", status, code)
	if message != "" {
		text += ": " + message
	}
	if env.RequestID != "" {
		text += " (request " + env.RequestID + ")"
	}
	if class == sink.Permanent {
		return sink.NewPermanent(code, errors.New(text))
	}
	return sink.NewRetryable(code, errors.New(text))
}

func classOf(status int) (sink.Class, string) {
	switch {
	case status == http.StatusUnauthorized:
		return sink.Permanent, "invalid_api_key"
	case status == http.StatusForbidden:
		return sink.Permanent, "forbidden"
	case status == http.StatusRequestTimeout:
		return sink.Retryable, "timeout"
	case status == http.StatusTooManyRequests:
		return sink.Retryable, "rate_limited"
	case status >= 500:
		return sink.Retryable, "server_error"
	default:
		return sink.Permanent, "invalid_request"
	}
}
```

`internal/sink/databox/client.go`:

```go
// Package databox is the first sink: a thin client for the Databox REST API v1,
// the two dataset record shapes, and the Sink that delivers spooled snapshots.
package databox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/sulcer/gaugewire/internal/sink"
)

// DefaultBaseURL is the public API host.
const DefaultBaseURL = "https://api.databox.com"

// MaxRecords is the documented maximum per ingestion event.
const MaxRecords = 100

const maxBody = 1 << 20

// Client calls the v1 endpoints Gaugewire needs. It never logs and never
// includes the key in an error.
type Client struct {
	base string
	key  string
	http *http.Client
}

// Account is one Databox account reachable with the key.
type Account struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	AccountType string `json:"accountType"`
}

// DataSource is an ingestion data source.
type DataSource struct {
	ID                 int64  `json:"id"`
	Title              string `json:"title"`
	Timezone           string `json:"timezone"`
	IngestionSupported bool   `json:"ingestionSupported"`
}

// Dataset is a container for ingested rows.
type Dataset struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// IngestionMetrics counts what one ingestion did.
type IngestionMetrics struct {
	Appended    int `json:"appendedRecordsCount"`
	Received    int `json:"receivedRecordsCount"`
	Rejected    int `json:"rejectedRecordsCount"`
	Overwritten int `json:"overwrittenRecordsCount"`
}

// Ingestion is the status of one ingestion event.
type Ingestion struct {
	ID        string `json:"ingestionId"`
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
	Metrics   IngestionMetrics
}

// NewClient builds a client for baseURL with the given key. A nil httpClient
// uses a default one; per-request timeouts come from the context.
func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("databox: base URL is empty")
	}
	if apiKey == "" {
		return nil, errors.New("databox: API key is empty")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), key: apiKey, http: httpClient}, nil
}

// ValidateKey confirms the key with GET /v1/auth/validate-key.
func (c *Client) ValidateKey(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/v1/auth/validate-key", nil, nil)
}

// Accounts lists the accounts the key can reach.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var out struct {
		Accounts []Account `json:"accounts"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/accounts", nil, &out); err != nil {
		return nil, err
	}
	return out.Accounts, nil
}

// DataSources lists an account's data sources.
func (c *Client) DataSources(ctx context.Context, accountID int64) ([]DataSource, error) {
	var out struct {
		DataSources []DataSource `json:"dataSources"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/accounts/"+strconv.FormatInt(accountID, 10)+"/data-sources", nil, &out); err != nil {
		return nil, err
	}
	return out.DataSources, nil
}

// CreateDataSource creates an ingestion data source in the account.
func (c *Client) CreateDataSource(ctx context.Context, accountID int64, title, timezone string) (DataSource, error) {
	body := map[string]any{"accountId": accountID, "title": title, "timezone": timezone}
	var out DataSource
	if err := c.do(ctx, http.MethodPost, "/v1/data-sources", body, &out); err != nil {
		return DataSource{}, err
	}
	return out, nil
}

// Datasets lists a data source's datasets.
func (c *Client) Datasets(ctx context.Context, dataSourceID int64) ([]Dataset, error) {
	var out struct {
		Datasets []Dataset `json:"datasets"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/data-sources/"+strconv.FormatInt(dataSourceID, 10)+"/datasets", nil, &out); err != nil {
		return nil, err
	}
	return out.Datasets, nil
}

// CreateDataset creates a dataset with the given primary keys. Column types
// are not part of the documented request; they are inferred from ingestion.
func (c *Client) CreateDataset(ctx context.Context, dataSourceID int64, title string, primaryKeys []string) (Dataset, error) {
	body := map[string]any{"dataSourceId": dataSourceID, "primaryKeys": primaryKeys, "title": title}
	var out Dataset
	if err := c.do(ctx, http.MethodPost, "/v1/datasets", body, &out); err != nil {
		return Dataset{}, err
	}
	return out, nil
}

// Ingest posts one ingestion event and returns its id.
func (c *Client) Ingest(ctx context.Context, datasetID string, records []map[string]any) (string, error) {
	if len(records) > MaxRecords {
		return "", sink.NewPermanent("too_many_records", fmt.Errorf("databox: %d records exceed the limit of %d per ingestion", len(records), MaxRecords))
	}
	var out struct {
		IngestionID string `json:"ingestionId"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/datasets/"+datasetID+"/data", map[string]any{"records": records}, &out); err != nil {
		return "", err
	}
	if out.IngestionID == "" {
		return "", sink.NewRetryable("invalid_response", errors.New("databox: ingestion response has no ingestionId"))
	}
	return out.IngestionID, nil
}

// Ingestion fetches the status of one ingestion event.
func (c *Client) Ingestion(ctx context.Context, datasetID, ingestionID string) (Ingestion, error) {
	var out struct {
		ID        string `json:"ingestionId"`
		Status    string `json:"status"`
		Timestamp string `json:"timestamp"`
		Metrics   struct {
			IngestionMetrics IngestionMetrics `json:"ingestionMetrics"`
		} `json:"metrics"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/datasets/"+datasetID+"/ingestions/"+ingestionID, nil, &out); err != nil {
		return Ingestion{}, err
	}
	return Ingestion{ID: out.ID, Status: out.Status, Timestamp: out.Timestamp, Metrics: out.Metrics.IngestionMetrics}, nil
}

// do sends one request with the documented headers and a request timeout,
// classifies error responses, and decodes a success body into out.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, sink.RequestTimeout)
	defer cancel()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return sink.NewPermanent("invalid_request", fmt.Errorf("databox: encode request: %w", err))
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, payload)
	if err != nil {
		return sink.NewPermanent("invalid_request", fmt.Errorf("databox: build request: %w", err))
	}
	req.Header.Set("x-api-key", c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return sink.NewRetryable("transport", fmt.Errorf("databox: %s %s: %w", method, path, err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return sink.NewRetryable("transport", fmt.Errorf("databox: read response: %w", err))
	}
	if resp.StatusCode >= 400 {
		return classify(resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return sink.NewRetryable("invalid_response", fmt.Errorf("databox: decode response: %w", err))
	}
	return nil
}
```

`url.Error` from `http.Client.Do` prints the URL, which holds the host and path but never the header, so the key cannot leak through it. The `Ingestion` struct's `Metrics` field carries no JSON tag on purpose: the wire shape nests it under `metrics.ingestionMetrics`, which the method flattens.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/sink/databox/` then `make check`.
Expected: `ok`, lint clean. If gosec flags G107 (variable URL) on `NewRequestWithContext`, add `//nolint:gosec // G107: the base URL comes from config.json` on that line.

- [ ] **Step 5: Commit**

```bash
git add internal/sink/databox
git commit -m "$(cat <<'EOF'
feat: add the databox v1 client and error classification

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Record shapes

**Files:**
- Create: `internal/sink/databox/records.go`, `internal/sink/databox/records_test.go`, `internal/sink/databox/testdata/history_record.golden`, `internal/sink/databox/testdata/current_record.golden`, `internal/sink/databox/testdata/history_record_unknown.golden`

**Interfaces:**
- Consumes: `quota.Snapshot`, `quota.Window`, `quota.WindowObserved` etc.
- Produces: `databox.HistoryTitle` ("Claude Quota History"), `databox.CurrentTitle` ("Claude Quota Current"), `databox.HistoryPrimaryKey` ("event_id"), `databox.CurrentPrimaryKey` ("account_id"), `databox.DataSourceTitle` ("Gaugewire"), `databox.HistoryRecord(s quota.Snapshot, eventType string, publishedAt time.Time) map[string]any`, `databox.CurrentRecord(s quota.Snapshot, lastSeenAt time.Time) map[string]any`, unexported `timestamp(t time.Time) string` (RFC 3339 UTC with milliseconds: layout `2006-01-02T15:04:05.000Z07:00`).

Every column is present in every record; unknown values are `nil`. Percentages are `float64`. The History columns, in this order for the reader (JSON objects are unordered): `event_id, event_type, account_id, account_alias, node_id, node_alias, platform, captured_at, published_at, five_hour_status, five_hour_used_percentage, five_hour_resets_at, seven_day_status, seven_day_used_percentage, seven_day_resets_at, source_type, claude_code_version, observer_version`. Current: `account_id, account_alias, node_id, node_alias, platform, latest_event_id, captured_at, last_seen_at, five_hour_status, five_hour_used_percentage, five_hour_resets_at, seven_day_status, seven_day_used_percentage, seven_day_resets_at, claude_code_version, observer_version`.

- [ ] **Step 1: Write the goldens and the failing tests**

`testdata/history_record.golden` (one line, no trailing newline; `json.Marshal` of a map sorts keys):

```
{"account_alias":"claude-01","account_id":"0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d","captured_at":"2026-09-17T15:30:00.000Z","claude_code_version":"2.1.274","event_id":"evt-1","event_type":"change","five_hour_resets_at":"2026-09-17T18:20:00.000Z","five_hour_status":"observed","five_hour_used_percentage":24,"node_alias":"mac-mini-01","node_id":"6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f","observer_version":"1.0.0","platform":"darwin","published_at":"2026-09-17T15:30:05.000Z","seven_day_resets_at":"2026-09-18T09:00:00.000Z","seven_day_status":"observed","seven_day_used_percentage":53.5,"source_type":"claude-code-statusline"}
```

`testdata/current_record.golden`:

```
{"account_alias":"claude-01","account_id":"0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d","captured_at":"2026-09-17T15:30:00.000Z","claude_code_version":"2.1.274","five_hour_resets_at":"2026-09-17T18:20:00.000Z","five_hour_status":"observed","five_hour_used_percentage":24,"last_seen_at":"2026-09-17T15:30:05.000Z","latest_event_id":"evt-1","node_alias":"mac-mini-01","node_id":"6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f","observer_version":"1.0.0","platform":"darwin","seven_day_resets_at":"2026-09-18T09:00:00.000Z","seven_day_status":"observed","seven_day_used_percentage":53.5}
```

`testdata/history_record_unknown.golden` (both windows unknown, no Claude Code version):

```
{"account_alias":"claude-01","account_id":"0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d","captured_at":"2026-09-17T15:30:00.000Z","claude_code_version":null,"event_id":"evt-2","event_type":"heartbeat","five_hour_resets_at":null,"five_hour_status":"unknown","five_hour_used_percentage":null,"node_alias":"mac-mini-01","node_id":"6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f","observer_version":"1.0.0","platform":"darwin","published_at":"2026-09-17T15:30:05.000Z","seven_day_resets_at":null,"seven_day_status":"unknown","seven_day_used_percentage":null,"source_type":"claude-code-statusline"}
```

`records_test.go`:

```go
package databox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

var (
	capturedAt  = time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)
	publishedAt = time.Date(2026, 9, 17, 15, 30, 5, 0, time.UTC)
)

func observedSnapshot(eventID string) quota.Snapshot {
	used5, used7 := 24.0, 53.5
	reset5 := time.Date(2026, 9, 17, 18, 20, 0, 0, time.UTC)
	reset7 := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	state := quota.NewState()
	state.Windows = quota.Windows{
		FiveHour: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used5, ResetsAt: &reset5},
		SevenDay: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used7, ResetsAt: &reset7},
	}
	state.ClaudeCodeVersion = "2.1.274"
	return quota.NewSnapshot(identity(), state, eventID, capturedAt)
}

func unknownSnapshot(eventID string) quota.Snapshot {
	return quota.NewSnapshot(identity(), quota.NewState(), eventID, capturedAt)
}

func identity() quota.Identity {
	return quota.Identity{
		NodeID: "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", NodeAlias: "mac-mini-01", Platform: "darwin",
		AccountID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", AccountAlias: "claude-01", ObserverVersion: "1.0.0",
	}
}

func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return string(data)
}

func encoded(t *testing.T, record map[string]any) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestHistoryRecordHasEveryColumn(t *testing.T) {
	t.Parallel()
	got := encoded(t, HistoryRecord(observedSnapshot("evt-1"), "change", publishedAt))
	if want := golden(t, "history_record.golden"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestHistoryRecordWritesNullForUnknowns(t *testing.T) {
	t.Parallel()
	got := encoded(t, HistoryRecord(unknownSnapshot("evt-2"), "heartbeat", publishedAt))
	if want := golden(t, "history_record_unknown.golden"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCurrentRecordHasEveryColumn(t *testing.T) {
	t.Parallel()
	got := encoded(t, CurrentRecord(observedSnapshot("evt-1"), publishedAt))
	if want := golden(t, "current_record.golden"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
```

`quota.NewState()` sets `ClaudeCodeVersion` to `""`; the unknown golden maps that to `null`. Check `quota.State`'s fields with `go doc ./internal/quota State` before assuming.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/sink/databox/`
Expected: FAIL to build, `undefined: HistoryRecord`, `undefined: CurrentRecord`.

- [ ] **Step 3: Write `records.go`**

```go
package databox

import (
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

// Titles and primary keys of the resources bootstrap creates.
const (
	DataSourceTitle   = "Gaugewire"
	HistoryTitle      = "Claude Quota History"
	CurrentTitle      = "Claude Quota Current"
	HistoryPrimaryKey = "event_id"
	CurrentPrimaryKey = "account_id"
)

const timestampLayout = "2006-01-02T15:04:05.000Z07:00"

// HistoryRecord is one row of the History dataset: the whole snapshot plus
// the event type and the send time. Every column is present; unknown values
// are null.
func HistoryRecord(s quota.Snapshot, eventType string, publishedAt time.Time) map[string]any {
	r := map[string]any{
		"event_id":     s.EventID,
		"event_type":   eventType,
		"published_at": timestamp(publishedAt),
		"source_type":  s.Source.Type,
	}
	for k, v := range common(s) {
		r[k] = v
	}
	return r
}

// CurrentRecord is the one row per subscription in the Current dataset.
func CurrentRecord(s quota.Snapshot, lastSeenAt time.Time) map[string]any {
	r := map[string]any{
		"latest_event_id": s.EventID,
		"last_seen_at":    timestamp(lastSeenAt),
	}
	for k, v := range common(s) {
		r[k] = v
	}
	return r
}

func common(s quota.Snapshot) map[string]any {
	r := map[string]any{
		"account_id":          s.Account.ID,
		"account_alias":       s.Account.Alias,
		"node_id":             s.Node.ID,
		"node_alias":          s.Node.Alias,
		"platform":            s.Node.Platform,
		"captured_at":         timestamp(s.CapturedAt),
		"claude_code_version": nullable(s.Source.ClaudeCodeVersion),
		"observer_version":    s.ObserverVersion,
	}
	window(r, "five_hour", s.Windows.FiveHour)
	window(r, "seven_day", s.Windows.SevenDay)
	return r
}

func window(r map[string]any, prefix string, w quota.Window) {
	r[prefix+"_status"] = string(w.Status)
	r[prefix+"_used_percentage"] = nil
	r[prefix+"_resets_at"] = nil
	if w.UsedPercentage != nil {
		r[prefix+"_used_percentage"] = *w.UsedPercentage
	}
	if w.ResetsAt != nil {
		r[prefix+"_resets_at"] = timestamp(*w.ResetsAt)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func timestamp(t time.Time) string {
	return t.UTC().Format(timestampLayout)
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/sink/databox/` then `make check`.
Expected: `ok`. `json.Marshal` of `24.0` prints `24`, which the golden expects.

- [ ] **Step 5: Commit**

```bash
git add internal/sink/databox
git commit -m "$(cat <<'EOF'
feat: map snapshots to the history and current dataset records

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Ingestion store and the Databox sink

**Files:**
- Create: `internal/sink/ingestion.go`, `internal/sink/ingestion_test.go`, `internal/sink/databox/sink.go`, `internal/sink/databox/sink_test.go`

**Interfaces:**
- Consumes: `store.Lock`, `store.LoadState`, `store.SaveState`, `store.IngestionRecord`, `store.StateLockFile`, `store.ErrStateCorrupt`; `databox.Client`, `HistoryRecord`, `CurrentRecord`; `quota.Snapshot`.
- Produces in `sink`: `Ingestion{Current, History string; CurrentCapturedAt *time.Time; At time.Time}`; `IngestionStore` interface `{ LoadIngestion(sinkID string) (Ingestion, bool, error); SaveIngestion(sinkID string, ing Ingestion) error }`; `StateIngestions{Home string}` implementing it under `state.lock` with a 1 s wait (a corrupt state file is treated as fresh and logged by the caller; `SaveIngestion` merges into `State.LastIngestion`).
- Produces in `databox`: `Sink` with `New(id string, client *Client, datasets Datasets, ingestions sink.IngestionStore, now func() time.Time, logger *slog.Logger) (*Sink, error)` (empty dataset ids → error); `Datasets{History, Current string}`; `(*Sink) ID() string`; `(*Sink) PublishBatch(ctx, snapshots []quota.Snapshot) error`.

`PublishBatch` semantics: `now := s.now().UTC()`; History records for every snapshot with the event type... the event type is not on `quota.Snapshot`; the flusher hands snapshots only. Ruling for this plan: `HistoryRecord` receives the event type from a new `sink.Sink` contract? No: keep the `Sink` interface unchanged and derive the event type as follows: the spooled `store.Event` carries `EventType`, but `PublishBatch` receives `[]quota.Snapshot`. Extend the interface minimally by adding the event type into the snapshot carrier: change `sink.Sink.PublishBatch(ctx, []sink.Delivery)` where `sink.Delivery{EventType string; Snapshot quota.Snapshot}` — that touches the flusher (`internal/sink/flusher.go` builds the slice from `events[i].Event.EventType` and `.Snapshot`), the flusher tests' `fakeSink` (record `d.Snapshot.EventID`), and nothing else. Do this change first in this task and keep it small.

Then: post `HistoryRecord(d.Snapshot, d.EventType, now)` for all deliveries; on error return it. Find the newest snapshot by `CapturedAt`; `ing, found, err := ingestions.LoadIngestion(id)`; if `err != nil` log Warn and treat as not found; if `!found || ing.CurrentCapturedAt == nil || newest.CapturedAt.After(*ing.CurrentCapturedAt)` post `CurrentRecord(newest, now)` and remember its id, else skip and keep the previous `Current` id and captured time. Save `Ingestion{Current, History, CurrentCapturedAt, At: now}`; a save error is logged at Warn and does not fail the batch (the API already accepted the data).

- [ ] **Step 1: Change the delivery type and the flusher**

In `internal/sink/sink.go` add:

```go
// Delivery is one spooled event handed to a sink: its type and the snapshot.
type Delivery struct {
	EventType string
	Snapshot  quota.Snapshot
}
```

and change `PublishBatch(ctx context.Context, deliveries []Delivery) error` in the interface. In `flusher.go` build `[]Delivery{{EventType: string(events[i].Event.EventType), Snapshot: events[i].Event.Snapshot}}`. In `flusher_test.go` the `fakeSink` records `d.Snapshot.EventID`. Run `go test -race -count=1 ./internal/sink/` and `go build ./...`: green, and `internal/cli/sinks.go` needs no change.

- [ ] **Step 2: Write the failing tests**

`internal/sink/ingestion_test.go`:

```go
package sink

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/store"
)

func TestStateIngestionsRoundTrip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	s := StateIngestions{Home: home}
	_, foundBefore, errBefore := s.LoadIngestion("databox-main")
	captured := time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)
	at := captured.Add(5 * time.Second)
	ing := Ingestion{Current: "ing-c", History: "ing-h", CurrentCapturedAt: &captured, At: at}
	errSave := s.SaveIngestion("databox-main", ing)
	got, found, errAfter := s.LoadIngestion("databox-main")
	state, _ := store.LoadState(home)
	type outcome struct {
		foundBefore, found bool
		errs               [3]bool
		ing                Ingestion
		stored             store.IngestionRecord
	}
	o := outcome{foundBefore, found, [3]bool{errBefore != nil, errSave != nil, errAfter != nil}, got, state.LastIngestion["databox-main"]}
	want := outcome{false, true, [3]bool{}, ing, store.IngestionRecord{Current: "ing-c", History: "ing-h", CurrentCapturedAt: &captured, At: at}}
	if diff := cmp.Diff(want, o, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestSaveIngestionKeepsOtherSinksAndQuotaState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	state := store.NewState()
	state.ClaudeCodeVersion = "2.1.274"
	state.LastIngestion = map[string]store.IngestionRecord{"other": {History: "keep"}}
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	s := StateIngestions{Home: home}
	if err := s.SaveIngestion("databox-main", Ingestion{History: "ing-h", At: time.Date(2026, 9, 17, 15, 30, 5, 0, time.UTC)}); err != nil {
		t.Fatalf("SaveIngestion: %v", err)
	}
	after, _ := store.LoadState(home)
	got := struct {
		version string
		other   string
		main    string
	}{after.ClaudeCodeVersion, after.LastIngestion["other"].History, after.LastIngestion["databox-main"].History}
	want := struct {
		version string
		other   string
		main    string
	}{"2.1.274", "keep", "ing-h"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

`internal/sink/databox/sink_test.go`:

```go
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
	mu   sync.Mutex
	recs map[string]sink.Ingestion
	fail error
}

func (m *memIngestions) LoadIngestion(id string) (sink.Ingestion, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return sink.Ingestion{}, false, m.fail
	}
	r, ok := m.recs[id]
	return r, ok, nil
}

func (m *memIngestions) SaveIngestion(id string, ing sink.Ingestion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail != nil {
		return m.fail
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
		out = append(out, sink.Delivery{EventType: "change", Snapshot: s})
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
	ings := &memIngestions{fail: errors.New("lock timeout")}
	err := newSink(t, f, ings).PublishBatch(t.Context(), deliveries("evt-1"))
	if err != nil || len(f.seen()) != 2 {
		t.Fatalf("err=%v calls=%d, want nil and both posts", err, len(f.seen()))
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
```

This file needs no `quota` import (`observedSnapshot` lives in `records_test.go`, same package); drop it from the import block above.

- [ ] **Step 3: Run the tests and watch them fail**

Run: `go test ./internal/sink/ ./internal/sink/databox/`
Expected: FAIL to build, `undefined: StateIngestions`, `undefined: Ingestion`, `undefined: New`, `undefined: Datasets`, `undefined: Sink`.

- [ ] **Step 4: Write the implementation**

`internal/sink/ingestion.go`:

```go
package sink

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/sulcer/gaugewire/internal/store"
)

// Ingestion is what a sink last had accepted: the newest ingestion ids and
// the capture time of the snapshot that Current holds.
type Ingestion struct {
	Current           string
	History           string
	CurrentCapturedAt *time.Time
	At                time.Time
}

// IngestionStore reads and writes a sink's ingestion record.
type IngestionStore interface {
	LoadIngestion(sinkID string) (Ingestion, bool, error)
	SaveIngestion(sinkID string, ing Ingestion) error
}

// StateIngestions keeps ingestion records in state.json under state.lock.
type StateIngestions struct {
	Home string
}

const ingestionLockWait = time.Second

// LoadIngestion returns the record for sinkID, if any. A corrupt state file
// reads as no record.
func (s StateIngestions) LoadIngestion(sinkID string) (Ingestion, bool, error) {
	unlock, err := store.Lock(context.Background(), filepath.Join(s.Home, store.StateLockFile), ingestionLockWait)
	if err != nil {
		return Ingestion{}, false, err
	}
	defer func() { _ = unlock() }()
	state, err := store.LoadState(s.Home)
	if err != nil && !errors.Is(err, store.ErrStateCorrupt) {
		return Ingestion{}, false, err
	}
	rec, ok := state.LastIngestion[sinkID]
	if !ok {
		return Ingestion{}, false, nil
	}
	return Ingestion{Current: rec.Current, History: rec.History, CurrentCapturedAt: rec.CurrentCapturedAt, At: rec.At}, true, nil
}

// SaveIngestion merges the record into state.json without touching the rest.
func (s StateIngestions) SaveIngestion(sinkID string, ing Ingestion) error {
	unlock, err := store.Lock(context.Background(), filepath.Join(s.Home, store.StateLockFile), ingestionLockWait)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	state, err := store.LoadState(s.Home)
	if err != nil && !errors.Is(err, store.ErrStateCorrupt) {
		return err
	}
	if state.LastIngestion == nil {
		state.LastIngestion = map[string]store.IngestionRecord{}
	}
	state.LastIngestion[sinkID] = store.IngestionRecord{Current: ing.Current, History: ing.History, CurrentCapturedAt: ing.CurrentCapturedAt, At: ing.At}
	return store.SaveState(s.Home, state)
}
```

The `context.Background()` here is deliberate: the lock wait is bounded by the 1 s timeout and the flusher's own context already bounds the run; if the linter wants a context parameter, add `ctx context.Context` as the first argument of both interface methods and pass the batch context through.

`internal/sink/databox/sink.go`:

```go
package databox

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/sink"
)

// Datasets are the two dataset ids the sink writes to.
type Datasets struct {
	History string
	Current string
}

// Sink delivers snapshots to the History and Current datasets.
type Sink struct {
	id         string
	client     *Client
	datasets   Datasets
	ingestions sink.IngestionStore
	now        func() time.Time
	logger     *slog.Logger
}

// New builds the sink; both dataset ids must be set.
func New(id string, client *Client, datasets Datasets, ingestions sink.IngestionStore, now func() time.Time, logger *slog.Logger) (*Sink, error) {
	if datasets.History == "" || datasets.Current == "" {
		return nil, errors.New("databox: both dataset ids are required; run gaugewire databox bootstrap")
	}
	return &Sink{id: id, client: client, datasets: datasets, ingestions: ingestions, now: now, logger: logger}, nil
}

// ID returns the sink id from config.json.
func (s *Sink) ID() string { return s.id }

// PublishBatch writes every delivery to History, then the newest snapshot to
// Current unless Current already holds something newer, and records the
// ingestion ids. Both accepts are the acknowledgement; any failure is
// returned classified so the flusher retries or dead-letters the whole chunk.
func (s *Sink) PublishBatch(ctx context.Context, deliveries []sink.Delivery) error {
	if len(deliveries) == 0 {
		return nil
	}
	now := s.now().UTC()
	records := make([]map[string]any, 0, len(deliveries))
	newest := deliveries[0].Snapshot
	for _, d := range deliveries {
		records = append(records, HistoryRecord(d.Snapshot, d.EventType, now))
		if d.Snapshot.CapturedAt.After(newest.CapturedAt) {
			newest = d.Snapshot
		}
	}
	historyID, err := s.client.Ingest(ctx, s.datasets.History, records)
	if err != nil {
		return err
	}
	previous, found, err := s.ingestions.LoadIngestion(s.id)
	if err != nil {
		s.logger.Warn("ingestion record unreadable", "sink", s.id, "reason", err.Error())
		found = false
	}
	next := sink.Ingestion{Current: previous.Current, History: historyID, CurrentCapturedAt: previous.CurrentCapturedAt, At: now}
	if !found || previous.CurrentCapturedAt == nil || newest.CapturedAt.After(*previous.CurrentCapturedAt) {
		currentID, err := s.client.Ingest(ctx, s.datasets.Current, []map[string]any{CurrentRecord(newest, now)})
		if err != nil {
			return err
		}
		captured := newest.CapturedAt
		next.Current = currentID
		next.CurrentCapturedAt = &captured
	}
	if err := s.ingestions.SaveIngestion(s.id, next); err != nil {
		s.logger.Warn("ingestion record not saved", "sink", s.id, "reason", err.Error())
	}
	return nil
}

var _ sink.Sink = (*Sink)(nil)
```

The `quota` import is used by the `newest` variable's type; if the compiler reports it unused, name the type explicitly (`var newest quota.Snapshot = deliveries[0].Snapshot`).

- [ ] **Step 5: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/sink/... ./internal/cli/` then `make check`.
Expected: `ok`, lint clean. `go build ./...` proves `cli` still compiles against the changed interface.

- [ ] **Step 6: Commit**

```bash
git add internal/sink
git commit -m "$(cat <<'EOF'
feat: deliver snapshots to databox history and current datasets

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Key loading, sink wiring and an end-to-end flush

**Files:**
- Create: `internal/cli/apikey.go`, `internal/cli/apikey_test.go`, `internal/cli/flush_databox_test.go`
- Modify: `internal/cli/sinks.go`, `cmd/gaugewire/main_integration_test.go`

**Interfaces:**
- Produces: `loadAPIKey(creds config.Credentials, getenv func(string) string) (string, error)` (file first: trimmed content, error when the file exists but is empty; else the env variable named by `apiKeyEnv` or `config.DefaultAPIKeyEnv` when empty; error `ErrNoAPIKey` ("no Databox API key: set credentials.apiKeyFile or the environment variable") when neither yields a key); on Unix a key file whose mode allows group or world access is still read but returns the key together with a non-nil warning string through a second return value `warn string`; `buildSinks(cfg config.Config, home string, logger *slog.Logger) []sink.Sink` now constructs a `databox.Sink` per enabled sink of type `databox` (base URL from the sink entry or `databox.DefaultBaseURL`, `sink.StateIngestions{Home: home}`), logging and skipping a sink whose key is missing or whose dataset ids are absent (`"sink not built"` with `reason`).

- [ ] **Step 1: Write the failing tests**

`internal/cli/apikey_test.go`:

```go
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sulcer/gaugewire/internal/config"
)

func TestLoadAPIKeyPrefersTheFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "databox.key")
	if err := os.WriteFile(path, []byte("  file-key \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	key, warn, err := loadAPIKey(config.Credentials{APIKeyEnv: "GW_TEST_KEY", APIKeyFile: path}, func(string) string { return "env-key" })
	got := struct {
		key, warn string
		err       bool
	}{key, warn, err != nil}
	want := struct {
		key, warn string
		err       bool
	}{"file-key", "", false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadAPIKeyFallsBackToTheEnvironment(t *testing.T) {
	t.Parallel()
	key, warn, err := loadAPIKey(config.Credentials{}, func(name string) string {
		if name == config.DefaultAPIKeyEnv {
			return "env-key"
		}
		return ""
	})
	got := struct {
		key, warn string
		err       bool
	}{key, warn, err != nil}
	want := struct {
		key, warn string
		err       bool
	}{"env-key", "", false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLoadAPIKeyReportsNoKey(t *testing.T) {
	t.Parallel()
	_, _, err := loadAPIKey(config.Credentials{APIKeyEnv: "GW_UNSET"}, func(string) string { return "" })
	if !errors.Is(err, ErrNoAPIKey) {
		t.Fatalf("got %v, want ErrNoAPIKey", err)
	}
}

func TestLoadAPIKeyWarnsAboutAWorldReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	t.Parallel()
	path := filepath.Join(t.TempDir(), "databox.key")
	if err := os.WriteFile(path, []byte("k"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	key, warn, err := loadAPIKey(config.Credentials{APIKeyFile: path}, func(string) string { return "" })
	got := struct {
		key  string
		warn string
		err  bool
	}{key, warn, err != nil}
	want := struct {
		key  string
		warn string
		err  bool
	}{"k", "key file " + path + " is readable by others; use mode 0600", false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

`internal/cli/flush_databox_test.go` (end to end through `runFlush` with the fake API; uses `t.Setenv`, not parallel):

```go
package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

// fakeAPI answers the two ingestion endpoints and records the bodies it saw.
func fakeAPI(t *testing.T, historyStatus int, historyBody string) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b bytes.Buffer
		_, _ = b.ReadFrom(r.Body)
		bodies = append(bodies, r.Method+" "+r.URL.Path+" "+b.String())
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
	return srv, &bodies
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
	srv, bodies := fakeAPI(t, http.StatusOK, `{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`)
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
		requests int
	}{err != nil, stdout.String(), pending, dead, state.LastIngestion["databox-main"].History, state.LastIngestion["databox-main"].Current, len(*bodies)}
	want := struct {
		err      bool
		out      string
		pending  int
		dead     int
		history  string
		current  string
		requests int
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
```

Read `flush.go` first: if `runFlush` prints something before the summary on a retryable failure (it does not today), adjust `out`. `store.Counts` in the dead-letter case counts the dead-lettered `.json` ✓.

Integration test, append to `cmd/gaugewire/main_integration_test.go` (the `httptest` server lives in the test process and the child binary connects to it over loopback):

```go
func TestFlushThroughTheBinaryAgainstAFakeAPI(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/datasets/ds-hist/data":
			_, _ = w.Write([]byte(`{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`))
		case "/v1/datasets/ds-cur/data":
			_, _ = w.Write([]byte(`{"requestId":"r","status":"success","ingestionId":"ing-c","message":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	sinks := `[{"id":"databox-main","type":"databox","enabled":true,"baseUrl":` + strconv.Quote(srv.URL) + `,"accountId":123456,"dataSourceId":4754489,"currentDatasetId":"ds-cur","historyDatasetId":"ds-hist","credentials":{"apiKeyEnv":"GW_IT_DATABOX_KEY","apiKeyFile":""}}]`
	home := integrationHome(t, "", sinks)
	payload, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "statusline", "full.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "statusline")
	cmd.Env = append(os.Environ(), "GAUGEWIRE_HOME="+home, "GW_IT_DATABOX_KEY=it-key")
	cmd.Stdin = bytes.NewReader(payload)
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("statusline: %v", err)
	}
	waitForFlushRuns(t, home, 1)
	pending, _ := filepath.Glob(filepath.Join(home, "pending", "*.json"))
	state, _ := os.ReadFile(filepath.Join(home, "state.json"))
	got := struct {
		pending int
		ingested bool
	}{len(pending), strings.Contains(string(state), `"history":"ing-h"`) && strings.Contains(string(state), `"current":"ing-c"`)}
	want := struct {
		pending int
		ingested bool
	}{0, true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

Add `"net/http"`, `"net/http/httptest"` and `"strconv"` to that file's imports if missing. The detached flusher inherits the environment from `statusline`, which is how it finds the key.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL to build, `undefined: loadAPIKey`, `undefined: ErrNoAPIKey`; then after adding them, the flush tests fail because `buildSinks` builds nothing.

- [ ] **Step 3: Write the implementation**

`internal/cli/apikey.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/sulcer/gaugewire/internal/config"
)

// ErrNoAPIKey means neither the key file nor the environment holds a key.
var ErrNoAPIKey = errors.New("no Databox API key: set credentials.apiKeyFile or the environment variable")

// loadAPIKey reads the key from the configured file, else from the named
// environment variable. The key is returned to the caller and never logged.
// On Unix a key file readable by others yields a warning the caller may log.
func loadAPIKey(creds config.Credentials, getenv func(string) string) (key, warn string, err error) {
	if creds.APIKeyFile != "" {
		raw, readErr := os.ReadFile(creds.APIKeyFile)
		if readErr != nil {
			return "", "", fmt.Errorf("read key file: %w", readErr)
		}
		key = strings.TrimSpace(string(raw))
		if key == "" {
			return "", "", fmt.Errorf("key file %s is empty", creds.APIKeyFile)
		}
		if runtime.GOOS != "windows" {
			if info, statErr := os.Stat(creds.APIKeyFile); statErr == nil && info.Mode().Perm()&0o077 != 0 {
				warn = "key file " + creds.APIKeyFile + " is readable by others; use mode 0600"
			}
		}
		return key, warn, nil
	}
	name := creds.APIKeyEnv
	if name == "" {
		name = config.DefaultAPIKeyEnv
	}
	if key = strings.TrimSpace(getenv(name)); key != "" {
		return key, "", nil
	}
	return "", "", ErrNoAPIKey
}
```

`internal/cli/sinks.go`:

```go
package cli

import (
	"log/slog"
	"os"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/sink"
	"github.com/sulcer/gaugewire/internal/sink/databox"
)

// buildSinks turns the enabled sink configurations into sinks. A sink that
// cannot be built is logged with the reason and skipped; its events stay
// spooled until the next run.
func buildSinks(cfg config.Config, home string, logger *slog.Logger) []sink.Sink {
	sinks := make([]sink.Sink, 0, len(cfg.Sinks))
	for _, s := range cfg.Sinks {
		if !s.Enabled {
			continue
		}
		built, err := buildDataboxSink(s, home, logger)
		if err != nil {
			logger.Warn("sink not built", "sink", s.ID, "reason", err.Error())
			continue
		}
		sinks = append(sinks, built)
	}
	return sinks
}

func buildDataboxSink(s config.Sink, home string, logger *slog.Logger) (sink.Sink, error) {
	key, warn, err := loadAPIKey(s.Credentials, os.Getenv)
	if err != nil {
		return nil, err
	}
	if warn != "" {
		logger.Warn(warn, "sink", s.ID)
	}
	base := s.BaseURL
	if base == "" {
		base = databox.DefaultBaseURL
	}
	client, err := databox.NewClient(base, key, nil)
	if err != nil {
		return nil, err
	}
	return databox.New(s.ID, client, databox.Datasets{History: s.HistoryDatasetID, Current: s.CurrentDatasetID}, sink.StateIngestions{Home: home}, time.Now, logger)
}
```

Update the one call site in `flush.go` to `buildSinks(cfg, home, logger)`. `config.Validate` already rejects unknown sink types, so a switch on `s.Type` is unnecessary today; add it when a second type exists.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/cli/`, `go test -race -tags integration -count=1 ./cmd/gaugewire/`, then `make check`.
Expected: `ok`. The end-to-end integration test proves the detached flusher delivers through the real code path.

- [ ] **Step 5: Commit**

```bash
git add internal/cli cmd/gaugewire
git commit -m "$(cat <<'EOF'
feat: build the databox sink from config and deliver on flush

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: `gaugewire databox bootstrap`

**Files:**
- Create: `internal/cli/bootstrap.go`, `internal/cli/bootstrap_test.go`
- Modify: `internal/cli/cli.go` (case `databox` with subcommand `bootstrap`, usage line)

**Interfaces:**
- Produces: `runDatabox(ctx, args, info, streams) error` dispatching `bootstrap`; `bootstrapOptions{accountID int64; apiKeyFile string; testIngest bool; baseURL string; sinkID string; getenv func(string) string; now func() time.Time; httpClient *http.Client}`; `bootstrap(ctx, home string, opts bootstrapOptions, stdout io.Writer) error`; `ErrChooseAccount` ("several accounts are reachable; pass --account-id"). Flags: `--account-id n`, `--api-key-file path`, `--test-ingest`, `--base-url url` (default `databox.DefaultBaseURL`; documented for testing), `--sink-id` (default `databox-main`).

Flow (spec flowchart): resolve the key (`--api-key-file` → record its absolute path in the sink's `credentials.apiKeyFile`; else the sink's existing credentials; else the environment) → `ValidateKey` → `Accounts` (one → use it; several → `--account-id` must name one of them, else `ErrChooseAccount`; none → error) → `DataSources(account)` reuse the one titled `Gaugewire` else `CreateDataSource(account, "Gaugewire", "UTC")` → `Datasets(dataSource)` reuse by title else `CreateDataset` with the primary key → persist `accountId, dataSourceId, currentDatasetId, historyDatasetId, baseUrl, enabled: true` into the sink entry (create it if absent, keep other sinks) → `config.Save` → with `--test-ingest` build one heartbeat snapshot from the current state (`quota.NewSnapshot(cfg.QuotaIdentity(runtime.GOOS, info.Version), state.State, uuid.NewV4().String(), now)`) and `PublishBatch` it through the real sink, printing the ingestion ids. Existing ids in config are verified against the lists (a configured dataset id that is not in the list is replaced by the title lookup and reported).

Output lines, exactly:

```
account:          <name> (<id>)
data source:      Gaugewire (<id>)
history dataset:  <id>
current dataset:  <id>
```

plus, with `--test-ingest`, `test ingest:      history <ingestionId>, current <ingestionId>`. When a resource was created, the line ends with ` created`; when reused, ` reused`.

- [ ] **Step 1: Write the failing tests**

`internal/cli/bootstrap_test.go` (all use the `fake` pattern; copy a minimal scripted server into this package as `fakeDatabox` in the test file since `databox`'s fake is test-only):

```go
package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/store"
)

type fakeDatabox struct {
	mu      sync.Mutex
	replies map[string][]string // "METHOD /path" -> bodies in order (last repeats), status 200
	fail    map[string]int      // "METHOD /path" -> status for the error envelope
	calls   []string
	server  *httptest.Server
}

func newFakeDatabox(t *testing.T) *fakeDatabox {
	t.Helper()
	f := &fakeDatabox{replies: map[string][]string{}, fail: map[string]int{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		key := r.Method + " " + r.URL.Path
		f.calls = append(f.calls, key)
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

func (f *fakeDatabox) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
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
	return bootstrapOptions{baseURL: f.server.URL, sinkID: "databox-main", getenv: func(string) string { return "boot-key" }, now: func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) }, httpClient: f.server.Client()}
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
	err := bootstrap(t.Context(), home, bootstrapOpts(f), &stdout)
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
		sinks: []config.Sink{{ID: "databox-main", Type: "databox", Enabled: true, BaseURL: f.server.URL, AccountID: 123456, DataSourceID: 4754489, CurrentDatasetID: "ds-cur", HistoryDatasetID: "ds-hist", Credentials: config.Credentials{APIKeyEnv: config.DefaultAPIKeyEnv}}},
		calls: []string{"GET /v1/auth/validate-key", "GET /v1/accounts", "GET /v1/accounts/123456/data-sources", "POST /v1/data-sources", "GET /v1/data-sources/4754489/datasets", "POST /v1/datasets", "POST /v1/datasets"},
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
	err := bootstrap(t.Context(), home, bootstrapOpts(f), &stdout)
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
		calls: []string{"GET /v1/auth/validate-key", "GET /v1/accounts", "GET /v1/accounts/123456/data-sources", "GET /v1/data-sources/4754489/datasets"},
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
	err := bootstrap(t.Context(), freshHome(t), bootstrapOpts(f), &bytes.Buffer{})
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
	err := bootstrap(t.Context(), home, opts, &bytes.Buffer{})
	cfg, _ := config.Load(home)
	if err != nil || cfg.Sinks[0].AccountID != 7 {
		t.Fatalf("err=%v accountId=%d, want nil and 7", err, cfg.Sinks[0].AccountID)
	}
}

func TestBootstrapStopsOnAnInvalidKeyWithoutWriting(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.fail["GET /v1/auth/validate-key"] = http.StatusUnauthorized
	home := freshHome(t)
	err := bootstrap(t.Context(), home, bootstrapOpts(f), &bytes.Buffer{})
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
	err := bootstrap(t.Context(), home, opts, &bytes.Buffer{})
	cfg, _ := config.Load(home)
	if err != nil || cfg.Sinks[0].Credentials.APIKeyFile != keyFile || cfg.Sinks[0].Credentials.APIKeyEnv != "" {
		t.Fatalf("err=%v creds=%+v, want the key file recorded", err, cfg.Sinks[0].Credentials)
	}
}

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
	err := bootstrap(t.Context(), freshHome(t), opts, &stdout)
	if err != nil || !strings.HasSuffix(stdout.String(), "test ingest:      history ing-h, current ing-c\n") {
		t.Fatalf("err=%v out=%q", err, stdout.String())
	}
}
```

Add `"os"` and `"path/filepath"` to the imports. `testConfig()` (from `observe_test.go`) has no sinks, which is the empty starting point.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL to build, `undefined: bootstrap`, `undefined: bootstrapOptions`, `undefined: ErrChooseAccount`.

- [ ] **Step 3: Write `internal/cli/bootstrap.go`**

```go
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"uuid"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/sink"
	"github.com/sulcer/gaugewire/internal/sink/databox"
	"github.com/sulcer/gaugewire/internal/store"
)

// ErrChooseAccount means the key reaches several accounts and none was named.
var ErrChooseAccount = errors.New("several accounts are reachable; pass --account-id")

const defaultSinkID = "databox-main"

type bootstrapOptions struct {
	accountID  int64
	apiKeyFile string
	testIngest bool
	baseURL    string
	sinkID     string
	getenv     func(string) string
	now        func() time.Time
	httpClient *http.Client
	info       BuildInfo
}

func runDatabox(ctx context.Context, args []string, info BuildInfo, streams IO) error {
	if len(args) == 0 || args[0] != "bootstrap" {
		return fmt.Errorf("usage: gaugewire databox bootstrap [--account-id n] [--api-key-file path] [--test-ingest]: %w", ErrUsage)
	}
	flags := flag.NewFlagSet("databox bootstrap", flag.ContinueOnError)
	flags.SetOutput(streams.Stderr)
	opts := bootstrapOptions{getenv: os.Getenv, now: time.Now, info: info}
	flags.Int64Var(&opts.accountID, "account-id", 0, "account to use when the key reaches several")
	flags.StringVar(&opts.apiKeyFile, "api-key-file", "", "file holding the API key; its path is recorded in config.json")
	flags.BoolVar(&opts.testIngest, "test-ingest", false, "send one heartbeat event after bootstrapping")
	flags.StringVar(&opts.baseURL, "base-url", databox.DefaultBaseURL, "API base URL")
	flags.StringVar(&opts.sinkID, "sink-id", defaultSinkID, "sink id in config.json")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	return bootstrap(ctx, home, opts, streams.Stdout)
}

// bootstrap validates the key, picks the account, reuses or creates the data
// source and datasets, and records their ids in the sink entry. Repeated runs
// create nothing.
func bootstrap(ctx context.Context, home string, opts bootstrapOptions, stdout io.Writer) error {
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	if err := store.EnsureLayout(home); err != nil {
		return err
	}
	entry := sinkEntry(&cfg, opts.sinkID)
	if opts.apiKeyFile != "" {
		absolute, err := filepath.Abs(opts.apiKeyFile)
		if err != nil {
			return err
		}
		entry.Credentials = config.Credentials{APIKeyFile: absolute}
	} else if entry.Credentials.APIKeyFile == "" && entry.Credentials.APIKeyEnv == "" {
		entry.Credentials.APIKeyEnv = config.DefaultAPIKeyEnv
	}
	key, warn, err := loadAPIKey(entry.Credentials, opts.getenv)
	if err != nil {
		return err
	}
	if warn != "" {
		fmt.Fprintln(stdout, "warning: "+warn)
	}
	client, err := databox.NewClient(opts.baseURL, key, opts.httpClient)
	if err != nil {
		return err
	}
	if err := client.ValidateKey(ctx); err != nil {
		return fmt.Errorf("validate key: %w", err)
	}
	account, err := chooseAccount(ctx, client, opts.accountID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "account:          %s (%d)\n", account.Name, account.ID)
	source, created, err := ensureDataSource(ctx, client, account.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "data source:      %s (%d) %s\n", source.Title, source.ID, createdOrReused(created))
	history, createdH, err := ensureDataset(ctx, client, source.ID, databox.HistoryTitle, databox.HistoryPrimaryKey)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "history dataset:  %s %s\n", history.ID, createdOrReused(createdH))
	current, createdC, err := ensureDataset(ctx, client, source.ID, databox.CurrentTitle, databox.CurrentPrimaryKey)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "current dataset:  %s %s\n", current.ID, createdOrReused(createdC))
	entry.Type = config.SinkTypeDatabox
	entry.Enabled = true
	entry.BaseURL = opts.baseURL
	entry.AccountID = account.ID
	entry.DataSourceID = source.ID
	entry.HistoryDatasetID = history.ID
	entry.CurrentDatasetID = current.ID
	if err := config.Save(home, cfg); err != nil {
		return err
	}
	if !opts.testIngest {
		return nil
	}
	return testIngest(ctx, home, cfg, *entry, client, opts, stdout)
}

// sinkEntry returns the sink with the id, appending a new one when absent.
func sinkEntry(cfg *config.Config, id string) *config.Sink {
	for i := range cfg.Sinks {
		if cfg.Sinks[i].ID == id {
			return &cfg.Sinks[i]
		}
	}
	cfg.Sinks = append(cfg.Sinks, config.Sink{ID: id, Type: config.SinkTypeDatabox})
	return &cfg.Sinks[len(cfg.Sinks)-1]
}

func chooseAccount(ctx context.Context, client *databox.Client, wanted int64) (databox.Account, error) {
	accounts, err := client.Accounts(ctx)
	if err != nil {
		return databox.Account{}, fmt.Errorf("list accounts: %w", err)
	}
	switch {
	case len(accounts) == 0:
		return databox.Account{}, errors.New("the key reaches no account")
	case wanted != 0:
		for _, a := range accounts {
			if a.ID == wanted {
				return a, nil
			}
		}
		return databox.Account{}, fmt.Errorf("account %d is not reachable with this key", wanted)
	case len(accounts) == 1:
		return accounts[0], nil
	default:
		return databox.Account{}, ErrChooseAccount
	}
}

func ensureDataSource(ctx context.Context, client *databox.Client, accountID int64) (databox.DataSource, bool, error) {
	sources, err := client.DataSources(ctx, accountID)
	if err != nil {
		return databox.DataSource{}, false, fmt.Errorf("list data sources: %w", err)
	}
	for _, s := range sources {
		if s.Title == databox.DataSourceTitle {
			return s, false, nil
		}
	}
	created, err := client.CreateDataSource(ctx, accountID, databox.DataSourceTitle, "UTC")
	if err != nil {
		return databox.DataSource{}, false, fmt.Errorf("create data source: %w", err)
	}
	return created, true, nil
}

func ensureDataset(ctx context.Context, client *databox.Client, dataSourceID int64, title, primaryKey string) (databox.Dataset, bool, error) {
	datasets, err := client.Datasets(ctx, dataSourceID)
	if err != nil {
		return databox.Dataset{}, false, fmt.Errorf("list datasets: %w", err)
	}
	for _, d := range datasets {
		if d.Title == title {
			return d, false, nil
		}
	}
	created, err := client.CreateDataset(ctx, dataSourceID, title, []string{primaryKey})
	if err != nil {
		return databox.Dataset{}, false, fmt.Errorf("create dataset %q: %w", title, err)
	}
	return created, true, nil
}

func createdOrReused(created bool) string {
	if created {
		return "created"
	}
	return "reused"
}

// testIngest sends one heartbeat built from the current state and prints the
// ingestion ids, so the dashboard shows a row before Claude Code runs.
func testIngest(ctx context.Context, home string, cfg config.Config, entry config.Sink, client *databox.Client, opts bootstrapOptions, stdout io.Writer) error {
	state, err := store.LoadState(home)
	if err != nil && !errors.Is(err, store.ErrStateCorrupt) {
		return err
	}
	s, err := databox.New(entry.ID, client, databox.Datasets{History: entry.HistoryDatasetID, Current: entry.CurrentDatasetID}, sink.StateIngestions{Home: home}, opts.now, logging.Discard())
	if err != nil {
		return err
	}
	snapshot := quota.NewSnapshot(cfg.QuotaIdentity(runtime.GOOS, opts.info.Version), state.State, uuid.NewV4().String(), opts.now())
	if err := s.PublishBatch(ctx, []sink.Delivery{{EventType: string(quota.EventHeartbeat), Snapshot: snapshot}}); err != nil {
		return fmt.Errorf("test ingest: %w", err)
	}
	ing, _, err := sink.StateIngestions{Home: home}.LoadIngestion(entry.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "test ingest:      history %s, current %s\n", ing.History, ing.Current)
	return nil
}
```

`ensureDataset` lists twice (once per dataset); acceptable for a command run once per machine. The `sinkEntry` pointer stays valid because `cfg.Sinks` is not reallocated after it is taken (the append happens inside `sinkEntry` before the pointer is returned). Add to `cli.go`: `case "databox": return runDatabox(ctx, args[1:], info, streams)` and the usage line `  databox bootstrap [--account-id n] [--api-key-file path] [--test-ingest]\n                      create or reuse the Databox data source and datasets`.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/cli/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "$(cat <<'EOF'
feat: add databox bootstrap

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Doctor's sink rows

**Files:**
- Modify: `internal/cli/doctor.go`, `internal/cli/doctor_checks.go`, `internal/cli/doctor_test.go`
- Create: `internal/cli/testdata/doctor_sink.golden`

**Interfaces:**
- Produces: three rows appended after `spool` when at least one enabled sink of type `databox` exists (each row per sink, named `sink auth (<id>)`, `datasets (<id>)`, `last ingestion (<id>)`); `doctorInput` gains `httpClient *http.Client` (nil → default) and `getenv func(string) string`; rows: sink auth → `ValidateKey` (✓ `key valid` / ✗ the classified error message, or ✗ `no API key: <reason>`); datasets → both configured ids present in `Datasets(dataSourceID)` (✓ `history <id>, current <id>` / ✗ `missing: <which>`); last ingestion → from `state.LastIngestion[id]`: none → ✓ `none yet`; else `Ingestion(history, id)` and `Ingestion(current, id)`: ✓ `history <status>, current <status>` when both statuses are `success` and both `Rejected == 0`, else ✗ with the statuses and rejected counts. Each call has the 15 s timeout from the client. `doctor` prints these rows only when a sink is enabled, so the offline goldens stay unchanged.

- [ ] **Step 1: Write the golden and the failing test**

`testdata/doctor_sink.golden` is `doctor_healthy.golden` with three lines inserted before the blank line:

```
✓ sink auth (databox-main): key valid
✓ datasets (databox-main): history ds-hist, current ds-cur
✓ last ingestion (databox-main): history success, current success
```

Test in `doctor_test.go`:

```go
func TestDoctorChecksTheSinkWhenEnabled(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/data-sources/4754489/datasets", bothDatasets)
	f.on("GET /v1/datasets/ds-hist/ingestions/ing-h", `{"requestId":"r","status":"success","ingestionId":"ing-h","timestamp":"x","metrics":{"ingestionMetrics":{"appendedRecordsCount":1,"receivedRecordsCount":1,"rejectedRecordsCount":0,"overwrittenRecordsCount":0}}}`)
	f.on("GET /v1/datasets/ds-cur/ingestions/ing-c", `{"requestId":"r","status":"success","ingestionId":"ing-c","timestamp":"x","metrics":{"ingestionMetrics":{"appendedRecordsCount":0,"receivedRecordsCount":1,"rejectedRecordsCount":0,"overwrittenRecordsCount":1}}}`)
	home, workDir, settingsPath := doctorHealthyFixture(t) // reuse the Task 5 (Plan 1c) fixture; adapt its signature if it differs
	cfg, _ := config.Load(home)
	cfg.Sinks = []config.Sink{{ID: "databox-main", Type: config.SinkTypeDatabox, Enabled: true, BaseURL: f.server.URL, AccountID: 123456, DataSourceID: 4754489, CurrentDatasetID: "ds-cur", HistoryDatasetID: "ds-hist", Credentials: config.Credentials{APIKeyEnv: "GW_DOCTOR_KEY"}}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	state, _ := store.LoadState(home)
	captured := doctorNow.Add(-2 * time.Minute)
	state.LastIngestion = map[string]store.IngestionRecord{"databox-main": {Current: "ing-c", History: "ing-h", CurrentCapturedAt: &captured, At: doctorNow.Add(-time.Minute)}}
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	in := doctorIn(t, home, settingsPath, workDir)
	in.httpClient = f.server.Client()
	in.getenv = func(string) string { return "doctor-key" }
	got := renderDoctor(diagnose(t.Context(), in))
	if want := doctorGolden(t, "doctor_sink.golden", home, workDir); got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}
```

Read the existing `doctorHealthyFixture` and `doctorIn` helpers first and adapt the call shapes to what they return; the intent is a healthy offline fixture plus an enabled sink and a saved ingestion record. Add a second test `TestDoctorReportsAnInvalidSinkKey` where `validate-key` fails with 401: the sink auth row is `✗ sink auth (databox-main): permanent (invalid_api_key): databox: HTTP 401 invalid_api_key: bad (request r)` and the verdict `UNHEALTHY`; build the expectation from the sink golden by replacing that line and the verdict (the other two rows still run and pass).

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL: the sink golden has three rows the output lacks (or a build failure on the new `doctorInput` fields).

- [ ] **Step 3: Implement the rows**

In `doctor_checks.go` add `checkSink(ctx, in doctorInput, s config.Sink) []check` returning the three rows, built on `loadAPIKey(s.Credentials, in.getenv)`, `databox.NewClient(base, key, in.httpClient)`, `client.ValidateKey`, `client.Datasets`, `client.Ingestion`, and `in.cfg`/`store.LoadState(in.home)` for the record. In `diagnose`, after the spool row, `for _, s := range in.cfg.Sinks { if s.Enabled { checks = append(checks, checkSink(ctx, in, s)...) } }`. `runDoctor` fills `httpClient: nil, getenv: os.Getenv`; `doctorIn` in tests fills `getenv` with a stub.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/cli/` then `make check`.
Expected: `ok`; the offline goldens are untouched.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "$(cat <<'EOF'
feat: let doctor check the databox sink over the network

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Docs and closing the plan

**Files:**
- Modify: `docs/spec/gaugewire/databox-sink.md`, `docs/spec/gaugewire/cli-and-install.md`, `docs/spec/gaugewire/data-contract.md`, `docs/spec/gaugewire/spool-and-flush.md`, `docs/spec/gaugewire/testing-strategy.md`, `docs/spec/gaugewire/architecture.md`, `docs/spec/README.md`, `docs/how-tos/acceptance-test.md`
- Delete: `docs/plans/2026-09-19-databox-sink.md`

- [ ] **Step 1: Make the pages state the tree**

Read each page against the code first.

- `databox-sink.md`: marker `Draft · Partial · 2026-09-19` (code built; acceptance test pending). Rewrite "API facts" to the fetched shapes listed in this plan's Spec line: dataset creation takes `title`, `dataSourceId`, `primaryKeys` and no column schema ("column types are not part of the documented request"); ingestion response fields; ingestion status metrics field names (`appendedRecordsCount`, `receivedRecordsCount`, `rejectedRecordsCount`, `overwrittenRecordsCount`); error envelope on 400 and 401; rate limits and the 500-row / 10 MB payload limit alongside the 100-record ingestion limit; accounts and data-source object fields. "Assumptions to verify": replace the schema assumption with "whether types are inferred from the first ingestion so that `*_at` columns behave as datetimes on dashboards", keep the 429 body assumption, add "status codes for errors other than 400 and 401". Delivery section: the flusher hands the sink deliveries (event type plus snapshot); `published_at` and `last_seen_at` are the send time; the `currentCapturedAt` guard skips Current on an equal capture time; the ingestion record is saved under `state.lock` after both accepts and a failure to save is logged, not retried. Credentials: `--api-key-file` records the file's absolute path; a file readable by others is used with a warning. Bootstrap: `--base-url` and `--sink-id` flags; output lines; idempotency proven by the reuse test.
- `cli-and-install.md`: Commands table row for `databox bootstrap` with its flags; the "Built / Not built" sentence becomes "Built: every command. `doctor`'s sink rows run only when a Databox sink is enabled and are the only network calls doctor makes, each with a 15 s timeout." doctor table: the three sink rows reference the endpoints as built; marker `Draft · Built · 2026-09-19` only if every sentence on the page is now true of the tree (check the Home directory row and the status example again); otherwise stay Partial and say why.
- `spool-and-flush.md`: step 7 records `lastFlush`; the sink records `lastIngestion` itself under `state.lock`; `Delivery` carries the event type.
- `data-contract.md`: `lastIngestion` is written by the Databox sink after both datasets accepted; `install.settingsPath` untouched; sink config `baseUrl` is set by bootstrap.
- `testing-strategy.md`: rows for `sink/databox` (scripted `httptest` fake, classification table, record goldens, sink tests), the `runFlush` end-to-end against `httptest`, the binary-level flush integration test, bootstrap tests; the acceptance test remains the manual proof.
- `architecture.md`: the layout line for `internal/sink/databox/` now says what exists; `lastIngestion` in the lifecycle diagram is written by the sink.
- `docs/spec/README.md`: gaugewire scope: "All v1 commands built; the acceptance test on a real machine is the remaining proof."
- `docs/how-tos/acceptance-test.md`: step 1.2 commands as built (`gaugewire install`, `gaugewire databox bootstrap --api-key-file <path> --test-ingest`), step 5 mentions `gaugewire doctor` for the ingestion rows, and the assumptions list matches the spec.

- [ ] **Step 2: Delete this plan, verify, commit**

```bash
git rm docs/plans/2026-09-19-databox-sink.md
```

Run the link check over touched files, the leak grep, and `make check`.

```bash
git add docs
git commit -m "$(cat <<'EOF'
docs: describe the databox sink as built

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

Then, with the owner's approval: push the branch and open the pull request with `gh pr create --assignee sulcer --label patch` and a Summary/Test plan body that names the acceptance test as the outstanding proof.
