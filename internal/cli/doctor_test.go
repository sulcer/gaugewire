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
	"github.com/sulcer/gaugewire/internal/source/claude"
	"github.com/sulcer/gaugewire/internal/store"
)

var doctorNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

// doctorGolden substitutes the temp paths into a golden. The settings
// placeholders are joined with filepath.Join so the goldens hold on Windows too.
func doctorGolden(t *testing.T, name, home, workDir string) string {
	t.Helper()
	g := golden(t, name)
	g = strings.ReplaceAll(g, "<localSettings>", filepath.Join(workDir, ".claude", "settings.local.json"))
	g = strings.ReplaceAll(g, "<projectSettings>", filepath.Join(workDir, ".claude", "settings.json"))
	return strings.ReplaceAll(strings.ReplaceAll(g, "<home>", home), "<workDir>", workDir)
}

// doctorHealthyFixture installs over a "cat" status line and saves a state that
// passes every row, so a test only has to break the row it is about.
func doctorHealthyFixture(t *testing.T) (home, settingsPath string) {
	t.Helper()
	home = t.TempDir()
	settingsPath = filepath.Join(t.TempDir(), "settings.json")
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
	return home, settingsPath
}

// doctorIn builds the input runDoctor would build, loading config.json from the
// fixture home the same way.
func doctorIn(t *testing.T, home, settingsPath, workDir string, runRenderer func(context.Context, string) error) doctorInput {
	t.Helper()
	cfg, cfgErr := config.Load(home)
	return doctorInput{home: home, settingsPath: settingsPath, workDir: workDir, now: doctorNow, runRenderer: runRenderer, cfg: cfg, cfgErr: cfgErr}
}

func TestDoctorHealthy(t *testing.T) {
	t.Parallel()
	home, settingsPath := doctorHealthyFixture(t)
	workDir := t.TempDir()
	in := doctorIn(t, home, settingsPath, workDir, func(context.Context, string) error { return nil })
	got := renderDoctor(diagnose(t.Context(), in))
	if want := doctorGolden(t, "doctor_healthy.golden", home, workDir); got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDoctorReportsDisableAllHooks(t *testing.T) {
	t.Parallel()
	home, settingsPath := doctorHealthyFixture(t)
	workDir := t.TempDir()
	projectSettings := filepath.Join(workDir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(projectSettings), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(projectSettings, []byte(`{"disableAllHooks": true}`), 0o600); err != nil {
		t.Fatalf("project: %v", err)
	}
	in := doctorIn(t, home, settingsPath, workDir, func(context.Context, string) error { return nil })
	got := renderDoctor(diagnose(t.Context(), in))
	want := doctorGolden(t, "doctor_healthy.golden", home, workDir)
	want = strings.Replace(want, "✓ overrides: none in "+workDir, "✗ overrides: "+projectSettings+" sets disableAllHooks", 1)
	want = strings.Replace(want, "\nHEALTHY\n", "\nUNHEALTHY\n", 1)
	if got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// Claude Code reads the layers local, project, user and the first that defines
// a setting wins, so a false in a higher layer hides a true in a lower one.
func TestDoctorIgnoresDisableAllHooksBelowTheLayerThatSetsItFalse(t *testing.T) {
	t.Parallel()
	home, settingsPath := doctorHealthyFixture(t)
	workDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workDir, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".claude", "settings.local.json"), []byte(`{"disableAllHooks": false}`), 0o600); err != nil {
		t.Fatalf("local: %v", err)
	}
	user := `{"disableAllHooks":true,"statusLine":{"type":"command","command":"/opt/gaugewire/bin/gaugewire statusline"}}`
	if err := os.WriteFile(settingsPath, []byte(user), 0o600); err != nil {
		t.Fatalf("user: %v", err)
	}
	in := doctorIn(t, home, settingsPath, workDir, func(context.Context, string) error { return nil })
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
	in := doctorIn(t, home, settingsPath, workDir, func(context.Context, string) error { return errors.New("exit 3: renderer: exit status 3") })
	got := renderDoctor(diagnose(t.Context(), in))
	if want := doctorGolden(t, "doctor_unhealthy.golden", home, workDir); got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSamplePayloadParses(t *testing.T) {
	t.Parallel()
	_, issues, err := claude.Parse(strings.NewReader(samplePayload), doctorNow)
	type outcome struct {
		issues int
		failed bool
	}
	got := outcome{len(issues), err != nil}
	if want := (outcome{0, false}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
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

// The HOME override keeps settings.DefaultPath() away from the real user
// settings file: this test must pass or fail on the recorded path alone.
func TestRunDoctorUsesTheRecordedSettingsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	t.Setenv("HOME", t.TempDir())
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	var stdout bytes.Buffer
	err := runDoctor(t.Context(), nil, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	type outcome struct {
		failed  bool
		healthy bool
	}
	got := outcome{err != nil, strings.HasSuffix(stdout.String(), "\nHEALTHY\n")}
	if want := (outcome{false, true}); got != want {
		t.Fatalf("got %+v, want %+v; stdout:\n%s", got, want, stdout.String())
	}
}
