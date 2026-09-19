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
