package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/store"
)

func TestOpenWritesJSONLinesToTheLogFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	logger, closeLog, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	logger.Info("event published", "eventId", "evt-1")
	if closeErr := closeLog(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
	raw, err := os.ReadFile(filepath.Join(home, store.LogsDir, FileName))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line); err != nil {
		t.Fatalf("log line is not JSON: %v: %q", err, raw)
	}
	got := map[string]any{"msg": line["msg"], "eventId": line["eventId"], "level": line["level"]}
	want := map[string]any{"msg": "event published", "eventId": "evt-1", "level": "INFO"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("log line mismatch (-want +got):\n%s", diff)
	}
}

func TestOpenRotatesAFullLogFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, store.LogsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	full := make([]byte, rotateAt)
	if err := os.WriteFile(filepath.Join(dir, FileName), full, 0o600); err != nil {
		t.Fatalf("seed full log: %v", err)
	}
	logger, closeLog, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	logger.Info("after rotation")
	_ = closeLog()
	current, _ := os.ReadFile(filepath.Join(dir, FileName))
	rotated, _ := os.Stat(filepath.Join(dir, FileName+".1"))
	var rotatedSize int64
	if rotated != nil {
		rotatedSize = rotated.Size()
	}
	type outcome struct {
		RotatedSize       int64
		CurrentHasLine    bool
		CurrentBelowLimit bool
	}
	got := outcome{rotatedSize, strings.Contains(string(current), "after rotation"), len(current) < rotateAt}
	want := outcome{int64(rotateAt), true, true}
	if got != want {
		t.Fatalf("rotation outcome mismatch: got %+v, want %+v", got, want)
	}
}

func TestDiscardIsDisabledAtEveryLevel(t *testing.T) {
	t.Parallel()
	if Discard().Handler().Enabled(t.Context(), slog.LevelError) {
		t.Fatal("discard logger must not be enabled")
	}
}
