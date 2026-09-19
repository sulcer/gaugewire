package databox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

var (
	capturedAt  = time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)
	publishedAt = time.Date(2026, 9, 17, 15, 30, 5, 0, time.UTC)
)

func observedSnapshot(eventID string) quota.Snapshot {
	used5, used7 := 24.0, 53.5
	reset5 := time.Date(2026, 9, 17, 18, 20, 0, 0, time.UTC)
	reset7 := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	state := quota.NewState()
	state.Windows = quota.Windows{
		FiveHour: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used5, ResetsAt: &reset5},
		SevenDay: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used7, ResetsAt: &reset7},
	}
	state.ClaudeCodeVersion = "2.1.274"
	return quota.NewSnapshot(identity(), state, eventID, capturedAt)
}

func unknownSnapshot(eventID string) quota.Snapshot {
	return quota.NewSnapshot(identity(), quota.NewState(), eventID, capturedAt)
}

func identity() quota.Identity {
	return quota.Identity{
		NodeID: "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", NodeAlias: "mac-mini-01", Platform: "darwin",
		AccountID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", AccountAlias: "claude-01", ObserverVersion: "1.0.0",
	}
}

func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return string(data)
}

func encoded(t *testing.T, record map[string]any) string {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}

func TestHistoryRecordHasEveryColumn(t *testing.T) {
	t.Parallel()
	got := encoded(t, HistoryRecord(observedSnapshot("evt-1"), "change", publishedAt))
	if want := golden(t, "history_record.golden"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestHistoryRecordWritesNullForUnknowns(t *testing.T) {
	t.Parallel()
	got := encoded(t, HistoryRecord(unknownSnapshot("evt-2"), "heartbeat", publishedAt))
	if want := golden(t, "history_record_unknown.golden"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestCurrentRecordHasEveryColumn(t *testing.T) {
	t.Parallel()
	got := encoded(t, CurrentRecord(observedSnapshot("evt-1"), publishedAt))
	if want := golden(t, "current_record.golden"); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
