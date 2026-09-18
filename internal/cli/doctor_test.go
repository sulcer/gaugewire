package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

var doctorNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func doctorGolden(t *testing.T, name, home, workDir string) string {
	t.Helper()
	g := golden(t, name)
	return strings.ReplaceAll(strings.ReplaceAll(g, "<home>", home), "<workDir>", workDir)
}

func TestDoctorHealthy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	workDir := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"cat"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	state := store.NewState()
	observed := doctorNow.Add(-2 * time.Minute)
	used := 24.0
	reset := doctorNow.Add(time.Hour)
	state.Windows = quota.Windows{
		FiveHour: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used, ResetsAt: &reset},
		SevenDay: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used, ResetsAt: &reset},
	}
	state.LastObservedAt = &observed
	state.ClaudeCodeVersion = "2.1.274"
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	in := doctorInput{home: home, settingsPath: settingsPath, workDir: workDir, now: doctorNow, runRenderer: func(context.Context, string) error { return nil }}
	got := renderDoctor(diagnose(t.Context(), in))
	if want := doctorGolden(t, "doctor_healthy.golden", home, workDir); got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDoctorUnhealthy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	workDir := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"cat","refreshInterval":5}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"jq -r .model.display_name","refreshInterval":5}}`), 0o600); err != nil {
		t.Fatalf("change: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".claude", "settings.local.json"), []byte(`{"statusLine":{"type":"command","command":"echo local"}}`), 0o600); err != nil {
		t.Fatalf("local: %v", err)
	}
	state := store.NewState()
	observed := doctorNow.Add(-5 * time.Hour)
	state.LastObservedAt = &observed
	state.ClaudeCodeVersion = "2.1.100"
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	at := doctorNow.Add(-time.Hour)
	for _, id := range []string{"evt-1", "evt-2"} {
		ev := store.Event{EventType: quota.EventChange, Snapshot: quota.Snapshot{SchemaVersion: 1, EventID: id, CapturedAt: at}, Delivery: map[string]store.DeliveryState{"databox-main": {NextAttemptAt: at}}}
		if _, err := store.WritePending(home, ev); err != nil {
			t.Fatalf("spool: %v", err)
		}
		at = at.Add(time.Minute)
	}
	pending, _ := store.ListPending(home)
	if err := store.DeadLetter(home, pending[0], "databox-main: permanent (invalid_api_key): 401", doctorNow.Add(-30*time.Minute)); err != nil {
		t.Fatalf("dead-letter: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, store.DeadLetterDir, "1789657000000-evt-bad.json.unreadable"), []byte("{"), 0o600); err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	in := doctorInput{home: home, settingsPath: settingsPath, workDir: workDir, now: doctorNow, runRenderer: func(context.Context, string) error { return errors.New("exit 3: renderer: exit status 3") }}
	got := renderDoctor(diagnose(t.Context(), in))
	if want := doctorGolden(t, "doctor_unhealthy.golden", home, workDir); got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunDoctorExitsUnhealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(home, testConfig()); err != nil {
		t.Fatalf("save: %v", err)
	}
	var stdout bytes.Buffer
	err := runDoctor(t.Context(), []string{"--settings", settingsPath}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if !errors.Is(err, ErrUnhealthy) || !strings.HasSuffix(stdout.String(), "\nUNHEALTHY\n") || !strings.Contains(stdout.String(), "✗ status-line integration: not installed") {
		t.Fatalf("err=%v stdout=%q", err, stdout.String())
	}
}

func TestRunDoctorHealthyReturnsNil(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"cat"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	var stdout bytes.Buffer
	err := runDoctor(t.Context(), []string{"--settings", settingsPath}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if err != nil || !strings.HasSuffix(stdout.String(), "\nHEALTHY\n") {
		t.Fatalf("err=%v stdout=%q", err, stdout.String())
	}
}
