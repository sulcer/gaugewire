package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/quota"
)

func TestLoadStateReturnsAFreshStateWhenTheFileIsMissing(t *testing.T) {
	t.Parallel()
	got, err := LoadState(t.TempDir())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if diff := cmp.Diff(NewState(), got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestSaveStateThenLoadStateRoundTrips(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	used := 24.0
	reset := time.Date(2026, 9, 17, 16, 20, 0, 0, time.UTC)
	captured := time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)
	want := NewState()
	want.Windows.FiveHour = quota.Window{Status: quota.WindowObserved, UsedPercentage: &used, ResetsAt: &reset}
	want.LastObservedAt = &captured
	want.ClaudeCodeVersion = "2.1.274"
	want.LastPublished = &quota.Published{EventID: "e1", CapturedAt: captured, Windows: want.Windows}
	want.LastFlush = &FlushRecord{At: captured, OK: true}
	want.LastIngestion = map[string]IngestionRecord{"sink-a": {Current: "ing-1", History: "ing-2", CurrentCapturedAt: &captured, At: captured}}
	if err := SaveState(home, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState(home)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadStateReportsCorruptionAndStartsFresh(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, StateFile), []byte(`{"schemaVersion":1,"windows":{"fiveHo`), 0o600); err != nil {
		t.Fatalf("write truncated state: %v", err)
	}
	got, err := LoadState(home)
	if !errors.Is(err, ErrStateCorrupt) {
		t.Fatalf("got error %v, want ErrStateCorrupt", err)
	}
	if diff := cmp.Diff(NewState(), got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadStateIgnoresAStrayTemporaryFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := SaveState(home, NewState()); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".tmp-123"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("write stray file: %v", err)
	}
	got, err := LoadState(home)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if diff := cmp.Diff(NewState(), got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestSaveStateWritesAnOwnerOnlyFile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("posix permissions")
	}
	home := t.TempDir()
	if err := SaveState(home, NewState()); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, StateFile))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("permissions %o, want no group or world bits", perm)
	}
}
