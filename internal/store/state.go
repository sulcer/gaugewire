package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

// StateFile is the name of the machine state inside the home directory.
const StateFile = "state.json"

// ErrStateCorrupt means state.json exists but could not be decoded; the caller
// received a fresh state and should log the condition.
var ErrStateCorrupt = errors.New("state.json is not valid; starting from a fresh state")

// FlushRecord is the outcome of the most recent flusher run.
type FlushRecord struct {
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
	Error string    `json:"error"`
}

// IngestionRecord remembers what a sink last accepted, keyed by sink id.
type IngestionRecord struct {
	Current           string     `json:"current"`
	History           string     `json:"history"`
	CurrentCapturedAt *time.Time `json:"currentCapturedAt"`
	At                time.Time  `json:"at"`
}

// State is the persisted machine state: the quota state plus delivery bookkeeping.
type State struct {
	quota.State
	LastFlush     *FlushRecord               `json:"lastFlush,omitempty"`
	LastIngestion map[string]IngestionRecord `json:"lastIngestion,omitempty"`
}

// NewState returns the state of a machine that has never observed anything.
func NewState() State {
	return State{State: quota.NewState()}
}

// LoadState reads state.json. A missing file yields a fresh state and no error;
// an undecodable file yields a fresh state and ErrStateCorrupt.
func LoadState(home string) (State, error) {
	raw, err := os.ReadFile(filepath.Join(home, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return NewState(), fmt.Errorf("read %s: %w", StateFile, err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return NewState(), fmt.Errorf("%w: %w", ErrStateCorrupt, err)
	}
	return s, nil
}

// SaveState writes state.json atomically with owner-only permissions.
func SaveState(home string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	return WriteFileAtomic(filepath.Join(home, StateFile), data, 0o600)
}
