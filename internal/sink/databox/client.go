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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sulcer/gaugewire/internal/sink"
)

// DefaultBaseURL is the public API host.
const DefaultBaseURL = "https://api.databox.com"

// MaxRecords is the documented maximum per ingestion event.
const MaxRecords = 100

const maxBody = 1 << 20

// errRedirect is what CheckRedirect returns; do reports it as permanent.
var errRedirect = errors.New("databox: redirects are not followed")

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
// uses a default one; per-request timeouts come from the context. The caller's
// client is copied rather than mutated.
func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		return nil, errors.New("databox: base URL is empty")
	}
	if apiKey == "" {
		return nil, errors.New("databox: API key is empty")
	}
	// The key travels in a header, so it only ever goes over TLS; plain http
	// is left for a test server on this machine.
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
	return &Client{base: strings.TrimRight(base, "/"), key: apiKey, http: &copied}, nil
}

// allowedScheme accepts https anywhere and plain http only to this machine.
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
	if err := c.do(ctx, http.MethodPost, "/v1/datasets/"+url.PathEscape(datasetID)+"/data", map[string]any{"records": records}, &out); err != nil {
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
	if err := c.do(ctx, http.MethodGet, "/v1/datasets/"+url.PathEscape(datasetID)+"/ingestions/"+url.PathEscape(ingestionID), nil, &out); err != nil {
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
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return sink.NewRetryable("invalid_response", fmt.Errorf("databox: decode response: %w", err))
	}
	return nil
}
