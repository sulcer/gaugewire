// Package databox is the first sink: a thin client for the Databox REST API v2,
// the two dataset record shapes, and the Sink that delivers spooled snapshots.
package databox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sulcer/gaugewire/internal/sink"
)

// DefaultBaseURL is the public API host.
const DefaultBaseURL = "https://api.databox.com"

// MaxRecords caps one ingestion event at the flusher's batch size, under the
// documented 500 rows per request.
const MaxRecords = 100

const maxBody = 1 << 20

// errRedirect is what CheckRedirect returns; do reports it as permanent.
var errRedirect = errors.New("databox: redirects are not followed")

// Client calls the v2 endpoints Gaugewire needs. It never logs and never
// includes the key in an error.
type Client struct {
	base      string
	key       string
	accountID int64
	http      *http.Client
}

// Organization is what the key, or the account it acts on, belongs to.
type Organization struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// DataSource is an ingestion data source.
type DataSource struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

// Dataset is a container for ingested rows.
type Dataset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Column declares one dataset column at creation; DataType is datetime,
// number or string.
type Column struct {
	ID       string `json:"id"`
	DataType string `json:"dataType"`
}

// IngestionSummary counts what one ingestion did.
type IngestionSummary struct {
	Received    int `json:"receivedRecordCount"`
	Appended    int `json:"appendedRecordCount"`
	Overwritten int `json:"overwrittenRecordCount"`
	Rejected    int `json:"rejectedRecordCount"`
}

// IngestionError is one record the ingestion could not store.
type IngestionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

// Ingestion is the status of one ingestion event.
type Ingestion struct {
	ID          string
	Status      string
	InitiatedAt string
	Summary     IngestionSummary
	Errors      []IngestionError
}

// NewClient builds a client for baseURL with the given key. A non-zero
// accountID acts on that account instead of the key's organization. A nil
// httpClient uses a default one; the caller's client is copied rather than
// mutated.
func NewClient(baseURL, apiKey string, accountID int64, httpClient *http.Client) (*Client, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		return nil, errors.New("databox: base URL is empty")
	}
	if apiKey == "" {
		return nil, errors.New("databox: API key is empty")
	}
	parsed, err := url.Parse(base)
	if err != nil || !allowedScheme(parsed) {
		return nil, errors.New("databox: base URL must use https")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	// Following a redirect would replay the x-api-key header to whatever host
	// the Location header names, so a redirect is a failed request instead.
	copied := *httpClient
	copied.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errRedirect
	}
	return &Client{base: strings.TrimRight(base, "/"), key: apiKey, accountID: accountID, http: &copied}, nil
}

// allowedScheme accepts https anywhere and plain http only to this machine,
// since the key travels in a header.
func allowedScheme(u *url.URL) bool {
	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := u.Hostname()
		ip := net.ParseIP(host)
		return strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	default:
		return false
	}
}

// ValidateKey confirms the key with GET /v2/auth/validate-key.
func (c *Client) ValidateKey(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/v2/auth/validate-key", nil, nil)
}

// Organization reads the organization the requests act on.
func (c *Client) Organization(ctx context.Context) (Organization, error) {
	var out Organization
	if err := c.do(ctx, http.MethodGet, "/v2/organization", nil, &out); err != nil {
		return Organization{}, err
	}
	return out, nil
}

// DataSources lists every data source of the organization.
func (c *Client) DataSources(ctx context.Context) ([]DataSource, error) {
	return list[DataSource](ctx, c, "/v2/data-sources", url.Values{})
}

// CreateDataSource creates an ingestion data source.
func (c *Client) CreateDataSource(ctx context.Context, name, timezone string) (DataSource, error) {
	body := map[string]any{"name": name, "timezone": timezone}
	var out DataSource
	if err := c.do(ctx, http.MethodPost, "/v2/data-sources", body, &out); err != nil {
		return DataSource{}, err
	}
	return out, nil
}

// Datasets lists a data source's datasets.
func (c *Client) Datasets(ctx context.Context, dataSourceID int64) ([]Dataset, error) {
	return list[Dataset](ctx, c, "/v2/datasets", url.Values{"dataSourceId": {strconv.FormatInt(dataSourceID, 10)}})
}

// CreateDataset creates a dataset with its primary key and column schema.
func (c *Client) CreateDataset(ctx context.Context, dataSourceID int64, name, primaryKey string, schema []Column) (Dataset, error) {
	body := map[string]any{"dataSourceId": dataSourceID, "name": name, "primaryKey": []string{primaryKey}, "schema": schema}
	var out Dataset
	if err := c.do(ctx, http.MethodPost, "/v2/datasets", body, &out); err != nil {
		return Dataset{}, err
	}
	return out, nil
}

// Ingest posts one ingestion event and returns its id.
func (c *Client) Ingest(ctx context.Context, datasetID int64, records []map[string]any) (string, error) {
	if len(records) > MaxRecords {
		return "", sink.NewPermanent("too_many_records", fmt.Errorf("databox: %d records exceed the limit of %d per ingestion", len(records), MaxRecords))
	}
	var out struct {
		IngestionID string `json:"ingestionId"`
	}
	if err := c.do(ctx, http.MethodPost, datasetPath(datasetID)+"/data", map[string]any{"records": records}, &out); err != nil {
		return "", err
	}
	if out.IngestionID == "" {
		return "", sink.NewRetryable("invalid_response", errors.New("databox: ingestion response has no ingestionId"))
	}
	return out.IngestionID, nil
}

// Ingestion fetches the status of one ingestion event.
func (c *Client) Ingestion(ctx context.Context, datasetID int64, ingestionID string) (Ingestion, error) {
	var out struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		InitiatedAt string `json:"initiatedAt"`
		Summary     struct {
			Ingestion IngestionSummary `json:"ingestion"`
		} `json:"summary"`
		Errors []IngestionError `json:"errors"`
	}
	if err := c.do(ctx, http.MethodGet, datasetPath(datasetID)+"/ingestions/"+url.PathEscape(ingestionID), nil, &out); err != nil {
		return Ingestion{}, err
	}
	return Ingestion{ID: out.ID, Status: out.Status, InitiatedAt: out.InitiatedAt, Summary: out.Summary.Ingestion, Errors: out.Errors}, nil
}

func datasetPath(id int64) string {
	return "/v2/datasets/" + strconv.FormatInt(id, 10)
}

// list reads every page of a list endpoint. Pages are 0-indexed and hold at
// most 100 items; an empty page also ends the walk, so a wrong totalItems
// cannot loop forever.
func list[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	var all []T
	for page := 0; ; page++ {
		query.Set("page", strconv.Itoa(page))
		query.Set("pageSize", "100")
		var out struct {
			Items      []T `json:"items"`
			Pagination struct {
				TotalItems int `json:"totalItems"`
			} `json:"pagination"`
		}
		if err := c.do(ctx, http.MethodGet, path+"?"+query.Encode(), nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Items...)
		if len(out.Items) == 0 || len(all) >= out.Pagination.TotalItems {
			return all, nil
		}
	}
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
	if c.accountID != 0 {
		req.Header.Set("x-account-id", strconv.FormatInt(c.accountID, 10))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if errors.Is(err, errRedirect) {
		return sink.NewPermanent("redirect", fmt.Errorf("databox: %s %s: %w", method, path, err))
	}
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
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return sink.NewRetryable("invalid_response", fmt.Errorf("databox: decode response: %w", err))
	}
	// The API documents error codes arriving in a 2xx body too.
	if env.Status == "error" {
		return classify(resp.StatusCode, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return sink.NewRetryable("invalid_response", fmt.Errorf("databox: decode response data: %w", err))
	}
	return nil
}
