package databox

import (
	"context"
	"errors"
	"fmt"
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
	}{false, []call{{Method: "GET", Path: "/v1/auth/validate-key", Key: testKey, Accept: "application/json"}}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestAccountsDecodesTheList(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v1/accounts", 200, `{"requestId":"r","status":"success","accounts":[{"id":123456,"name":"Acme","accountType":"organization"}]}`)
	accounts, err := client(t, f).Accounts(t.Context())
	got := struct {
		err      bool
		accounts []Account
	}{err != nil, accounts}
	want := struct {
		err      bool
		accounts []Account
	}{false, []Account{{ID: 123456, Name: "Acme", AccountType: "organization"}}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
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
		id          string
		err         bool
		contentType string
		body        string
	}{id, err != nil, f.seen()[0].ContentType, f.seen()[0].Body}
	want := struct {
		id          string
		err         bool
		contentType string
		body        string
	}{"ing-1", false, "application/json", `{"records":[{"event_id":"evt-1","five_hour_used_percentage":24}]}`}
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
	type outcome struct {
		failed bool
		class  sink.Class
		code   string
		calls  int
	}
	got := outcome{err != nil, class, code, len(f.seen())}
	if want := (outcome{true, sink.Permanent, "too_many_records", 0}); got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
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

// errTransport fails every request, with no port and no network involved.
type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial")
}

func TestTransportFailureIsRetryable(t *testing.T) {
	t.Parallel()
	c, err := NewClient("https://example.test", testKey, &http.Client{Transport: errTransport{}})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	err = c.ValidateKey(t.Context())
	class, code := sink.Classify(err)
	type outcome struct {
		failed bool
		class  sink.Class
		code   string
	}
	got := outcome{err != nil, class, code}
	if want := (outcome{true, sink.Retryable, "transport"}); got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
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

func TestRedirectsAreNotFollowed(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.onRedirect("GET", "/v1/auth/validate-key", 302, f.server.URL+"/elsewhere")
	err := client(t, f).ValidateKey(t.Context())
	class, code := sink.Classify(err)
	type outcome struct {
		failed bool
		class  sink.Class
		code   string
		calls  int
	}
	got := outcome{err != nil, class, code, len(f.seen())}
	if want := (outcome{true, sink.Permanent, "redirect", 1}); got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

func TestNewClientRefusesPlainHTTPToARemoteHost(t *testing.T) {
	t.Parallel()
	_, errRemote := NewClient("http://api.example.test", testKey, nil)
	_, errLoopback := NewClient("http://127.0.0.1:1", testKey, nil)
	_, errLocalhost := NewClient("http://localhost:8080", testKey, nil)
	type outcome struct {
		remote              string
		loopback, localhost bool
	}
	got := outcome{fmt.Sprint(errRemote), errLoopback == nil, errLocalhost == nil}
	if want := (outcome{"databox: base URL must use https", true, true}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestIdsAreEscapedInThePath uses ids holding "?" and "#", which would end the
// path early if they were pasted in unescaped.
func TestIdsAreEscapedInThePath(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	c := client(t, f)
	_, _ = c.Ingest(t.Context(), "ds?x", []map[string]any{{"event_id": "evt-1"}})
	_, _ = c.Ingestion(t.Context(), "ds?x", "ing#1")
	want := []string{"POST /v1/datasets/ds?x/data", "GET /v1/datasets/ds?x/ingestions/ing#1"}
	if diff := cmp.Diff(want, paths(f.seen())); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestIngestWithoutAnIngestionIDIsRetryable(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v1/datasets/ds-hist/data", 200, `{"requestId":"r","status":"success"}`)
	_, err := client(t, f).Ingest(t.Context(), "ds-hist", []map[string]any{{"event_id": "evt-1"}})
	class, code := sink.Classify(err)
	if err == nil || class != sink.Retryable || code != "invalid_response" {
		t.Fatalf("err=%v class=%v code=%q, want retryable invalid_response", err, class, code)
	}
}
