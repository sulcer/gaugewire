package sink

import (
	"context"
	"errors"
	"fmt"
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

// ErrIngestionRecordCorrupt means state.json could not be decoded; the caller
// logs it and continues as if there were no record.
var ErrIngestionRecordCorrupt = errors.New("ingestion record unreadable")

// IngestionStore reads and writes a sink's ingestion record.
type IngestionStore interface {
	LoadIngestion(ctx context.Context, sinkID string) (Ingestion, bool, error)
	SaveIngestion(ctx context.Context, sinkID string, ing Ingestion) error
}

// StateIngestions keeps ingestion records in state.json under state.lock.
type StateIngestions struct {
	Home string
}

const ingestionLockWait = time.Second

// LoadIngestion returns the record for sinkID, if any. A corrupt state file is
// ErrIngestionRecordCorrupt; any other failure is retryable, since the record
// may well exist.
func (s StateIngestions) LoadIngestion(ctx context.Context, sinkID string) (Ingestion, bool, error) {
	unlock, err := store.Lock(ctx, filepath.Join(s.Home, store.StateLockFile), ingestionLockWait)
	if err != nil {
		return Ingestion{}, false, NewRetryable("state_lock", err)
	}
	defer func() { _ = unlock() }()
	state, err := store.LoadState(s.Home)
	if errors.Is(err, store.ErrStateCorrupt) {
		return Ingestion{}, false, fmt.Errorf("%w: %w", ErrIngestionRecordCorrupt, err)
	}
	if err != nil {
		return Ingestion{}, false, NewRetryable("state_lock", err)
	}
	rec, ok := state.LastIngestion[sinkID]
	if !ok {
		return Ingestion{}, false, nil
	}
	return Ingestion{Current: rec.Current, History: rec.History, CurrentCapturedAt: rec.CurrentCapturedAt, At: rec.At}, true, nil
}

// SaveIngestion merges the record into state.json without touching the rest. A
// corrupt state file is overwritten from a fresh state, as the hot path does.
func (s StateIngestions) SaveIngestion(ctx context.Context, sinkID string, ing Ingestion) error {
	unlock, err := store.Lock(ctx, filepath.Join(s.Home, store.StateLockFile), ingestionLockWait)
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
