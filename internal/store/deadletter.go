package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// DeadLetterEntry summarises one dead-lettered event for status and doctor.
type DeadLetterEntry struct {
	EventID        string
	Reason         string
	DeadLetteredAt time.Time
}

// ListDeadLetters reads dead-letter/ newest first. Files quarantined as
// .unreadable are counted, never decoded.
func ListDeadLetters(home string) ([]DeadLetterEntry, int, error) {
	dir := filepath.Join(home, DeadLetterDir)
	dirEntries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", dir, err)
	}
	var entries []DeadLetterEntry
	unreadable := 0
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		switch {
		case strings.HasSuffix(name, ".unreadable"):
			unreadable++
		case strings.HasSuffix(name, ".json"):
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, 0, fmt.Errorf("read %s: %w", name, err)
			}
			var ev Event
			if err := json.Unmarshal(raw, &ev); err != nil {
				return nil, 0, fmt.Errorf("decode %s: %w", name, err)
			}
			entry := DeadLetterEntry{EventID: ev.Snapshot.EventID, Reason: ev.Reason}
			if ev.DeadLetteredAt != nil {
				entry.DeadLetteredAt = *ev.DeadLetteredAt
			}
			entries = append(entries, entry)
		}
	}
	slices.SortFunc(entries, func(a, b DeadLetterEntry) int {
		if !a.DeadLetteredAt.Equal(b.DeadLetteredAt) {
			return b.DeadLetteredAt.Compare(a.DeadLetteredAt)
		}
		return strings.Compare(a.EventID, b.EventID)
	})
	return entries, unreadable, nil
}
