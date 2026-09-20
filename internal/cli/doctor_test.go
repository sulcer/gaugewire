package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

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

// doctorHealthyFixture passes every row, so a test only breaks the one it is about.
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

func doctorIn(t *testing.T, home, settingsPath, workDir string, runRenderer func(context.Context, string) error) doctorInput {
	t.Helper()
	cfg, cfgErr := config.Load(home)
	return doctorInput{
		home: home, settingsPath: settingsPath, workDir: workDir, now: doctorNow, runRenderer: runRenderer,
		getenv: func(string) string { return "" }, cfg: cfg, cfgErr: cfgErr,
	}
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

const (
	historyIngestion = `{"requestId":"r","status":"success","ingestionId":"ing-h","timestamp":"x","metrics":{"ingestionMetrics":{"appendedRecordsCount":1,"receivedRecordsCount":1,"rejectedRecordsCount":0,"overwrittenRecordsCount":0}}}`
	currentIngestion = `{"requestId":"r","status":"success","ingestionId":"ing-c","timestamp":"x","metrics":{"ingestionMetrics":{"appendedRecordsCount":0,"receivedRecordsCount":1,"rejectedRecordsCount":0,"overwrittenRecordsCount":1}}}`
)

// doctorSinkFixture is the healthy fixture plus one enabled sink pointing at f
// and the ingestion record a flush would have left behind.
func doctorSinkFixture(t *testing.T, f *fakeDatabox) (home, settingsPath, workDir string) {
	t.Helper()
	home, settingsPath = doctorHealthyFixture(t)
	workDir = t.TempDir()
	f.on("GET /v1/data-sources/4754489/datasets", bothDatasets)
	f.on("GET /v1/datasets/ds-hist/ingestions/ing-h", historyIngestion)
	f.on("GET /v1/datasets/ds-cur/ingestions/ing-c", currentIngestion)
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Sinks = []config.Sink{{
		ID: "databox-main", Type: config.SinkTypeDatabox, Enabled: true, BaseURL: f.server.URL,
		AccountID: 123456, DataSourceID: 4754489, CurrentDatasetID: "ds-cur", HistoryDatasetID: "ds-hist",
		Credentials: config.Credentials{APIKeyEnv: "GW_DOCTOR_KEY"},
	}}
	if saveErr := config.Save(home, cfg); saveErr != nil {
		t.Fatalf("save: %v", saveErr)
	}
	state, err := store.LoadState(home)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	captured := doctorNow.Add(-2 * time.Minute)
	state.LastIngestion = map[string]store.IngestionRecord{
		"databox-main": {Current: "ing-c", History: "ing-h", CurrentCapturedAt: &captured, At: doctorNow.Add(-time.Minute)},
	}
	if saveErr := store.SaveState(home, state); saveErr != nil {
		t.Fatalf("save state: %v", saveErr)
	}
	return home, settingsPath, workDir
}

func doctorSinkIn(t *testing.T, f *fakeDatabox, home, settingsPath, workDir string) doctorInput {
	t.Helper()
	in := doctorIn(t, home, settingsPath, workDir, func(context.Context, string) error { return nil })
	in.httpClient = f.server.Client()
	in.getenv = func(string) string { return "doctor-key" }
	return in
}

func TestDoctorChecksTheSinkWhenEnabled(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	got := renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir)))
	if want := doctorGolden(t, "doctor_sink.golden", home, workDir); got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDoctorReportsAnInvalidSinkKey(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.failWith("GET /v1/auth/validate-key", http.StatusUnauthorized)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	got := renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir)))
	want := doctorGolden(t, "doctor_sink.golden", home, workDir)
	want = strings.Replace(want,
		"✓ sink auth (databox-main): key valid",
		"✗ sink auth (databox-main): permanent (invalid_api_key): databox: HTTP 401 invalid_api_key: bad (request r)", 1)
	want = strings.Replace(want, "\nHEALTHY\n", "\nUNHEALTHY\n", 1)
	if got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDoctorReportsRejectedRecords(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/datasets/ds-cur/ingestions/ing-c", strings.Replace(currentIngestion, `"rejectedRecordsCount":0`, `"rejectedRecordsCount":2`, 1))
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	got := renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir)))
	want := doctorGolden(t, "doctor_sink.golden", home, workDir)
	want = strings.Replace(want,
		"✓ last ingestion (databox-main): history success, current success",
		"✗ last ingestion (databox-main): history success, current success (2 rejected)", 1)
	want = strings.Replace(want, "\nHEALTHY\n", "\nUNHEALTHY\n", 1)
	if got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDoctorReportsAMissingDataset(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	f.on("GET /v1/data-sources/4754489/datasets", `{"requestId":"r","status":"success","datasets":[{"id":"ds-hist","title":"Claude Quota History","created":"x"}]}`)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	got := renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir)))
	want := doctorGolden(t, "doctor_sink.golden", home, workDir)
	want = strings.Replace(want,
		"✓ datasets (databox-main): history ds-hist, current ds-cur",
		"✗ datasets (databox-main): missing: current ds-cur", 1)
	want = strings.Replace(want, "\nHEALTHY\n", "\nUNHEALTHY\n", 1)
	if got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDoctorReportsNoIngestionYet(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	state, err := store.LoadState(home)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	delete(state.LastIngestion, "databox-main")
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	got := renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir)))
	want := doctorGolden(t, "doctor_sink.golden", home, workDir)
	want = strings.Replace(want,
		"✓ last ingestion (databox-main): history success, current success",
		"✓ last ingestion (databox-main): none yet", 1)
	if got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// All three rows go through the same unresolved key, so each must read
// ErrNoAPIKey's text unwrapped rather than wrapped a second time.
func TestDoctorReportsAMissingKey(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	in := doctorIn(t, home, settingsPath, workDir, func(context.Context, string) error { return nil })
	in.httpClient = f.server.Client()
	got := struct {
		out  string
		seen int
	}{renderDoctor(diagnose(t.Context(), in)), len(f.seen())}
	const msg = "no Databox API key: pass --api-key-file to gaugewire databox bootstrap, or set the environment variable named by credentials.apiKeyEnv (default DATABOX_API_KEY)"
	want := doctorGolden(t, "doctor_sink.golden", home, workDir)
	want = strings.Replace(want, "✓ sink auth (databox-main): key valid", "✗ sink auth (databox-main): "+msg, 1)
	want = strings.Replace(want, "✓ datasets (databox-main): history ds-hist, current ds-cur", "✗ datasets (databox-main): "+msg, 1)
	want = strings.Replace(want, "✓ last ingestion (databox-main): history success, current success", "✗ last ingestion (databox-main): "+msg, 1)
	want = strings.Replace(want, "\nHEALTHY\n", "\nUNHEALTHY\n", 1)
	if diff := cmp.Diff(struct {
		out  string
		seen int
	}{want, 0}, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestDoctorReportsAKeyFileWarningOnAValidKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	keyFile := filepath.Join(t.TempDir(), "databox.key")
	if err := os.WriteFile(keyFile, []byte("doctor-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	// WriteFile's mode is masked by the umask; chmod is not.
	if err := os.Chmod(keyFile, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Sinks[0].Credentials = config.Credentials{APIKeyFile: keyFile}
	if err := config.Save(home, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir)))
	want := strings.Replace(doctorGolden(t, "doctor_sink.golden", home, workDir),
		"✓ sink auth (databox-main): key valid",
		"✓ sink auth (databox-main): key valid; key file "+keyFile+" is readable by others; use mode 0600", 1)
	if got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

// The config.json is written past config.Save, which would refuse it: its sink
// still decodes, but doctor must not act on a configuration it rejected.
func TestDoctorSkipsTheSinkRowsWhenTheConfigurationIsInvalid(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Publishing.MinDeltaPercentage = 0
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, config.File), raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	got := struct {
		out  string
		seen int
	}{renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir))), len(f.seen())}
	want := doctorGolden(t, "doctor_healthy.golden", home, workDir)
	want = strings.Replace(want, "✓ configuration: valid", "✗ configuration: config.json is invalid: publishing.minDeltaPercentage must be positive", 1)
	want = strings.Replace(want, "\nHEALTHY\n", "\nUNHEALTHY\n", 1)
	if diff := cmp.Diff(struct {
		out  string
		seen int
	}{want, 0}, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestDoctorSkipsADisabledSink(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Sinks[0].Enabled = false
	if err := config.Save(home, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := struct {
		out  string
		seen int
	}{renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir))), len(f.seen())}
	want := struct {
		out  string
		seen int
	}{doctorGolden(t, "doctor_healthy.golden", home, workDir), 0}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

// Both blanked ids must be answered without a request.
func TestDoctorReportsUnconfiguredIDs(t *testing.T) {
	t.Parallel()
	f := newFakeDatabox(t)
	f.on("GET /v1/auth/validate-key", validKey)
	home, settingsPath, workDir := doctorSinkFixture(t, f)
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	cfg.Sinks[0].DataSourceID = 0
	cfg.Sinks[0].HistoryDatasetID = ""
	if err := config.Save(home, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := struct {
		out  string
		seen []string
	}{renderDoctor(diagnose(t.Context(), doctorSinkIn(t, f, home, settingsPath, workDir))), f.seen()}
	want := doctorGolden(t, "doctor_sink.golden", home, workDir)
	want = strings.Replace(want,
		"✓ datasets (databox-main): history ds-hist, current ds-cur",
		"✗ datasets (databox-main): data source not configured", 1)
	want = strings.Replace(want,
		"✓ last ingestion (databox-main): history success, current success",
		"✗ last ingestion (databox-main): history not configured, current success", 1)
	want = strings.Replace(want, "\nHEALTHY\n", "\nUNHEALTHY\n", 1)
	if diff := cmp.Diff(struct {
		out  string
		seen []string
	}{want, []string{"GET /v1/auth/validate-key", "GET /v1/datasets/ds-cur/ingestions/ing-c"}}, got, cmp.AllowUnexported(got)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
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
