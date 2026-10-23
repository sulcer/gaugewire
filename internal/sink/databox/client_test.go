package databox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/sink"
)

const testKey = "dbx-test-key-0123"

func client(t *testing.T, f *fake) *Client {
	t.Helper()
	c, err := NewClient(f.server.URL, testKey, 0, f.server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestNewClientRefusesAnEmptyBaseURLOrKey(t *testing.T) {
	t.Parallel()
	_, errURL := NewClient("", "k", 0, nil)
	_, errKey := NewClient("https://example.test", "", 0, nil)
	if errURL == nil || errKey == nil {
		t.Fatalf("errURL=%v errKey=%v, want both non-nil", errURL, errKey)
	}
}

func TestValidateKeySendsTheHeader(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/auth/validate-key", 200, `{"requestId":"r1","status":"success"}`)
	err := client(t, f).ValidateKey(t.Context())
	got := struct {
		err   bool
		calls []call
	}{err != nil, f.seen()}
	want := struct {
		err   bool
		calls []call
	}{false, []call{{Method: "GET", Path: "/v2/auth/validate-key", Key: testKey, Accept: "application/json"}}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestAnAccountIDTravelsInTheAccountHeader(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/organization", 200, `{"requestId":"r","status":"success","data":{"id":4754489,"name":"Client A"}}`)
	c, err := NewClient(f.server.URL, testKey, 4754489, f.server.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, _ = c.Organization(t.Context())
	want := []call{{Method: "GET", Path: "/v2/organization", Key: testKey, Account: "4754489", Accept: "application/json"}}
	if diff := cmp.Diff(want, f.seen()); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestOrganizationDecodesTheData(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/organization", 200, `{"requestId":"r","status":"success","data":{"id":123456,"name":"Acme","companyName":"Acme, Inc."}}`)
	org, err := client(t, f).Organization(t.Context())
	want := Organization{ID: 123456, Name: "Acme"}
	if err != nil || org != want {
		t.Fatalf("got %+v err %v, want %+v", org, err, want)
	}
}

func TestDataSourcesAndCreate(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/data-sources", 200, `{"requestId":"r","status":"success","data":{"items":[{"id":4754489,"name":"Gaugewire","integrationKey":"DataboxAPI","timezone":"UTC"}],"pagination":{"page":0,"pageSize":100,"totalItems":1}}}`)
	f.on("POST", "/v2/data-sources", 200, `{"requestId":"r","status":"success","data":{"id":4754490,"name":"Gaugewire","integrationKey":"DataboxAPI","timezone":"UTC"}}`)
	c := client(t, f)
	listed, errList := c.DataSources(t.Context())
	created, errCreate := c.CreateDataSource(t.Context(), "Gaugewire", "UTC")
	type outcome struct {
		listed  []DataSource
		created DataSource
		errs    [2]bool
		query   string
		body    string
	}
	got := outcome{listed, created, [2]bool{errList != nil, errCreate != nil}, f.seen()[0].Query, f.seen()[1].Body}
	want := outcome{
		listed:  []DataSource{{ID: 4754489, Name: "Gaugewire", Timezone: "UTC"}},
		created: DataSource{ID: 4754490, Name: "Gaugewire", Timezone: "UTC"},
		query:   "page=0&pageSize=100",
		body:    `{"name":"Gaugewire","timezone":"UTC"}`,
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestDatasetsAndCreate(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/datasets", 200, `{"requestId":"r","status":"success","data":{"items":[{"id":901,"dataSourceId":4754489,"name":"Claude Quota History","ingestionSupported":true}],"pagination":{"page":0,"pageSize":100,"totalItems":1}}}`)
	f.on("POST", "/v2/datasets", 200, `{"requestId":"r","status":"success","data":{"id":902,"dataSourceId":4754489,"name":"Claude Quota Current","ingestionSupported":true}}`)
	c := client(t, f)
	listed, errList := c.Datasets(t.Context(), 4754489)
	schema := []Column{{ID: "account_id", DataType: "string"}, {ID: "captured_at", DataType: "datetime"}}
	created, errCreate := c.CreateDataset(t.Context(), 4754489, "Claude Quota Current", "account_id", schema)
	type outcome struct {
		listed  []Dataset
		created Dataset
		errs    [2]bool
		query   string
		body    string
	}
	got := outcome{listed, created, [2]bool{errList != nil, errCreate != nil}, f.seen()[0].Query, f.seen()[1].Body}
	want := outcome{
		listed:  []Dataset{{ID: 901, Name: "Claude Quota History"}},
		created: Dataset{ID: 902, Name: "Claude Quota Current"},
		query:   "dataSourceId=4754489&page=0&pageSize=100",
		body:    `{"dataSourceId":4754489,"name":"Claude Quota Current","primaryKey":["account_id"],"schema":[{"id":"account_id","dataType":"string"},{"id":"captured_at","dataType":"datetime"}]}`,
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

// Pages are 0-indexed and hold at most 100 items, so a list longer than one
// page is read until totalItems is reached.
func TestListsReadEveryPage(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/data-sources", 200, `{"requestId":"r","status":"success","data":{"items":[{"id":1,"name":"First"}],"pagination":{"page":0,"pageSize":1,"totalItems":2}}}`)
	f.on("GET", "/v2/data-sources", 200, `{"requestId":"r","status":"success","data":{"items":[{"id":2,"name":"Gaugewire"}],"pagination":{"page":1,"pageSize":1,"totalItems":2}}}`)
	listed, err := client(t, f).DataSources(t.Context())
	type outcome struct {
		listed  []DataSource
		err     bool
		queries []string
	}
	queries := make([]string, 0, 2)
	for _, c := range f.seen() {
		queries = append(queries, c.Query)
	}
	got := outcome{listed, err != nil, queries}
	want := outcome{
		listed:  []DataSource{{ID: 1, Name: "First"}, {ID: 2, Name: "Gaugewire"}},
		queries: []string{"page=0&pageSize=100", "page=1&pageSize=100"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

// Without totalItems only a short page ends the walk, so a full page is
// followed by a request for the next one.
func TestAListWithoutPaginationReadsOnUntilAShortPage(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	items := make([]string, 0, pageSize)
	want := make([]DataSource, 0, pageSize)
	for i := range pageSize {
		items = append(items, fmt.Sprintf(`{"id":%d,"name":"s%d"}`, i+1, i+1))
		want = append(want, DataSource{ID: int64(i + 1), Name: fmt.Sprintf("s%d", i+1)})
	}
	f.on("GET", "/v2/data-sources", 200, `{"requestId":"r","status":"success","data":{"items":[`+strings.Join(items, ",")+`]}}`)
	f.on("GET", "/v2/data-sources", 200, `{"requestId":"r","status":"success","data":{"items":[]}}`)
	listed, err := client(t, f).DataSources(t.Context())
	type outcome struct {
		listed []DataSource
		err    bool
		calls  int
	}
	got := outcome{listed, err != nil, len(f.seen())}
	if diff := cmp.Diff(outcome{want, false, 2}, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

// A server that ignores page keeps serving the first one; the walk gives up
// after maxPages requests instead of reading forever.
func TestAListThatNeverEndsFailsAfterMaxPages(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/data-sources", 200, `{"requestId":"r","status":"success","data":{"items":[{"id":1,"name":"Same"}],"pagination":{"page":0,"pageSize":100,"totalItems":250}}}`)
	_, err := client(t, f).DataSources(t.Context())
	class, code := sink.Classify(err)
	type outcome struct {
		failed bool
		class  sink.Class
		code   string
		calls  int
	}
	got := outcome{err != nil, class, code, len(f.seen())}
	if want := (outcome{true, sink.Retryable, "invalid_response", maxPages}); got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

// Key validation reads no payload: an empty body passes, but a body that is
// not the API's envelope, such as a proxy's login page, does not.
func TestValidateKeyAcceptsOnlyAnEmptyOrEnvelopeBody(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		code string
	}{
		{"empty", ``, ""},
		{"envelope", `{"requestId":"r","status":"success"}`, ""},
		{"html", `<html>sign in</html>`, "invalid_response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(t)
			f.on("GET", "/v2/auth/validate-key", 200, tt.body)
			_, code := sink.Classify(client(t, f).ValidateKey(t.Context()))
			if code != tt.code {
				t.Fatalf("code %q, want %q", code, tt.code)
			}
		})
	}
}

func TestIngestReturnsTheIngestionID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v2/datasets/901/data", 200, `{"requestId":"r","status":"success","data":{"ingestionId":"ing-1","status":"pending","message":"accepted"}}`)
	id, err := client(t, f).Ingest(t.Context(), 901, []map[string]any{{"event_id": "evt-1", "five_hour_used_percentage": 24.0}})
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
	_, err := client(t, f).Ingest(t.Context(), 901, records)
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

func TestIngestionDecodesTheSummaryAndErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/datasets/901/ingestions/ing-1", 200, `{"requestId":"r","status":"success","data":{"id":"ing-1","initiatedAt":"2026-10-23T10:00:00.000000+00:00","status":"success","duration":200,"summary":{"ingestion":{"receivedRecordCount":3,"appendedRecordCount":1,"overwrittenRecordCount":1,"rejectedRecordCount":1},"dataset":{"columnCount":18,"rowCount":40,"size":1024}},"errors":[{"code":"schema_mismatch","message":"captured_at is not a datetime","field":"captured_at","record":{"event_id":"evt-1"}}]}}`)
	got, err := client(t, f).Ingestion(t.Context(), 901, "ing-1")
	want := Ingestion{
		ID:          "ing-1",
		Status:      "success",
		InitiatedAt: "2026-10-23T10:00:00.000000+00:00",
		Summary:     IngestionSummary{Received: 3, Appended: 1, Overwritten: 1, Rejected: 1},
		Errors:      []IngestionError{{Code: "schema_mismatch", Message: "captured_at is not a datetime", Field: "captured_at"}},
	}
	if diff := cmp.Diff(want, got); err != nil || diff != "" {
		t.Fatalf("err %v, mismatch (-want +got):\n%s", err, diff)
	}
}

func TestErrorsNeverCarryTheKey(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("GET", "/v2/auth/validate-key", 401, `{"requestId":"req-9","status":"error","errors":[{"code":"invalid_api_key","message":"key rejected","field":"","type":"auth"}]}`)
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

// The API documents error codes arriving in a 2xx body; the code then decides
// whether a retry can help.
func TestAnErrorInASuccessResponseIsClassifiedByItsCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		code  string
		class sink.Class
	}{
		{"rate limited", "rate_limited", sink.Retryable},
		{"internal error", "internal_error", sink.Retryable},
		{"service unavailable", "service_unavailable", sink.Retryable},
		{"processing failed", "processing_failed", sink.Retryable},
		{"schema mismatch", "schema_mismatch", sink.Permanent},
		{"dataset limit", "dataset_limit_exceeded", sink.Permanent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(t)
			f.on("POST", "/v2/datasets/901/data", 200, `{"requestId":"req-7","status":"error","errors":[{"code":"`+tt.code+`","message":"no","field":"","type":"ingestion"}]}`)
			_, err := client(t, f).Ingest(t.Context(), 901, []map[string]any{{"event_id": "evt-1"}})
			class, code := sink.Classify(err)
			type outcome struct {
				failed bool
				class  sink.Class
				code   string
			}
			got := outcome{err != nil, class, code}
			if want := (outcome{true, tt.class, tt.code}); got != want {
				t.Fatalf("got %+v err %v, want %+v", got, err, want)
			}
		})
	}
}

// errTransport fails every request, with no port and no network involved.
type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial")
}

func TestTransportFailureIsRetryable(t *testing.T) {
	t.Parallel()
	c, err := NewClient("https://example.test", testKey, 0, &http.Client{Transport: errTransport{}})
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
	f.on("GET", "/v2/organization", 200, `<html>`)
	_, err := client(t, f).Organization(t.Context())
	class, code := sink.Classify(err)
	if err == nil || class != sink.Retryable || code != "invalid_response" || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v class=%v code=%q, want retryable invalid_response", err, class, code)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.onRedirect("GET", "/v2/auth/validate-key", 302, f.server.URL+"/elsewhere")
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
	_, errRemote := NewClient("http://api.example.test", testKey, 0, nil)
	_, errLoopback := NewClient("http://127.0.0.1:1", testKey, 0, nil)
	_, errLocalhost := NewClient("http://localhost:8080", testKey, 0, nil)
	type outcome struct {
		remote              string
		loopback, localhost bool
	}
	got := outcome{fmt.Sprint(errRemote), errLoopback == nil, errLocalhost == nil}
	if want := (outcome{"databox: base URL must use https", true, true}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// The ingestion id holds "#", which would end the path early if pasted unescaped.
func TestTheIngestionIDIsEscapedInThePath(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	_, _ = client(t, f).Ingestion(t.Context(), 901, "ing#1")
	want := []string{"GET /v2/datasets/901/ingestions/ing#1"}
	if diff := cmp.Diff(want, paths(f.seen())); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestIngestWithoutAnIngestionIDIsRetryable(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.on("POST", "/v2/datasets/901/data", 200, `{"requestId":"r","status":"success","data":{"status":"pending"}}`)
	_, err := client(t, f).Ingest(t.Context(), 901, []map[string]any{{"event_id": "evt-1"}})
	class, code := sink.Classify(err)
	if err == nil || class != sink.Retryable || code != "invalid_response" {
		t.Fatalf("err=%v class=%v code=%q, want retryable invalid_response", err, class, code)
	}
}
