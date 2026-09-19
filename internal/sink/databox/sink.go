package databox

import (
	"context"
	"errors"
	"log/slog"
	"time"

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
// The record is read before anything is posted: without it the Current guard
// cannot be applied, and failing then costs nothing, since History is an
// upsert that a retry simply resends.
func (s *Sink) PublishBatch(ctx context.Context, deliveries []sink.Delivery) error {
	if len(deliveries) == 0 {
		return nil
	}
	previous, found, err := s.ingestions.LoadIngestion(ctx, s.id)
	if errors.Is(err, sink.ErrIngestionRecordCorrupt) {
		s.logger.Warn("ingestion record unreadable", "sink", s.id, "reason", err.Error())
		previous, found = sink.Ingestion{}, false
	} else if err != nil {
		return err
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
	if err := s.ingestions.SaveIngestion(ctx, s.id, next); err != nil {
		s.logger.Warn("ingestion record not saved", "sink", s.id, "reason", err.Error())
	}
	return nil
}

var _ sink.Sink = (*Sink)(nil)
