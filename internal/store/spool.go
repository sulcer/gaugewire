package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

// DeliveryState tracks one sink's attempts to deliver an event.
type DeliveryState struct {
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	LastError     string    `json:"lastError"`
	LastErrorCode string    `json:"lastErrorCode"`
}

// Event is a spooled snapshot with its delivery bookkeeping. Delivery is keyed
// by the sink ids enabled when the event was captured.
type Event struct {
	EventType      quota.EventType          `json:"eventType"`
	Snapshot       quota.Snapshot           `json:"snapshot"`
	Delivery       map[string]DeliveryState `json:"delivery"`
	DeadLetteredAt *time.Time               `json:"deadLetteredAt,omitempty"`
	Reason         string                   `json:"reason,omitempty"`
}

// PendingEvent is an event together with the file that holds it.
type PendingEvent struct {
	Path  string
	Event Event
}

// WritePending stores a new event in pending/ and returns its path. The name
// sorts chronologically: <captured unix millis>-<event id>.json.
func WritePending(home string, ev Event) (string, error) {
	name := fmt.Sprintf("%d-%s.json", ev.Snapshot.CapturedAt.UnixMilli(), ev.Snapshot.EventID)
	path := filepath.Join(home, PendingDir, name)
	if err := writeEvent(path, ev); err != nil {
		return "", err
	}
	return path, nil
}

// ListPending returns every pending event in chronological order. A file that
// cannot be decoded fails the listing so the caller can report it.
func ListPending(home string) ([]PendingEvent, error) {
	return listEvents(filepath.Join(home, PendingDir))
}

// UpdatePending rewrites an event in place, atomically.
func UpdatePending(pe PendingEvent) error {
	return writeEvent(pe.Path, pe.Event)
}

// DeletePending removes a delivered event.
func DeletePending(pe PendingEvent) error {
	if err := os.Remove(pe.Path); err != nil {
		return fmt.Errorf("delete %s: %w", pe.Path, err)
	}
	return nil
}

// DeadLetter moves an event whose delivery failed permanently into dead-letter/,
// recording when and why. It is never deleted automatically.
func DeadLetter(home string, pe PendingEvent, reason string, now time.Time) error {
	ev := pe.Event
	at := now.UTC()
	ev.DeadLetteredAt = &at
	ev.Reason = reason
	target := filepath.Join(home, DeadLetterDir, filepath.Base(pe.Path))
	if err := writeEvent(target, ev); err != nil {
		return err
	}
	return DeletePending(pe)
}

// Requeue moves every dead-letter event back to pending/ with its delivery state
// reset, so the next flush retries it. It returns how many events were moved,
// even when it stops on an error.
func Requeue(home string, now time.Time) (int, error) {
	dead, err := listEvents(filepath.Join(home, DeadLetterDir))
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, pe := range dead {
		ev := pe.Event
		ev.DeadLetteredAt = nil
		ev.Reason = ""
		for sink := range ev.Delivery {
			ev.Delivery[sink] = DeliveryState{NextAttemptAt: now.UTC()}
		}
		if err := writeEvent(filepath.Join(home, PendingDir, filepath.Base(pe.Path)), ev); err != nil {
			return moved, err
		}
		if err := os.Remove(pe.Path); err != nil {
			return moved, fmt.Errorf("remove %s: %w", pe.Path, err)
		}
		moved++
	}
	return moved, nil
}

// Counts reports how many events wait in pending/ and dead-letter/.
func Counts(home string) (pending, dead int, err error) {
	pending, err = countEvents(filepath.Join(home, PendingDir))
	if err != nil {
		return 0, 0, err
	}
	dead, err = countEvents(filepath.Join(home, DeadLetterDir))
	if err != nil {
		return 0, 0, err
	}
	return pending, dead, nil
}

func writeEvent(path string, ev Event) error {
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	return WriteFileAtomic(path, data, 0o600)
}

func eventFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func listEvents(dir string) ([]PendingEvent, error) {
	names, err := eventFiles(dir)
	if err != nil {
		return nil, err
	}
	out := make([]PendingEvent, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		out = append(out, PendingEvent{Path: path, Event: ev})
	}
	return out, nil
}

func countEvents(dir string) (int, error) {
	names, err := eventFiles(dir)
	if err != nil {
		return 0, err
	}
	return len(names), nil
}
