package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if got["msg"] != want["msg"] || got["eventId"] != want["eventId"] || got["level"] != want["level"] {
		t.Fatalf("got %v, want %v", got, want)
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
	if rotated == nil || rotated.Size() != int64(rotateAt) || !strings.Contains(string(current), "after rotation") || len(current) >= rotateAt {
		t.Fatalf("rotation failed: current=%d bytes, rotated=%v", len(current), rotated)
	}
}

func TestDiscardIsDisabledAtEveryLevel(t *testing.T) {
	t.Parallel()
	if Discard().Handler().Enabled(t.Context(), slog.LevelError) {
		t.Fatal("discard logger must not be enabled")
	}
}
