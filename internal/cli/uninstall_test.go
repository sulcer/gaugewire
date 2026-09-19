package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/store"
)

func installFixture(t *testing.T, original string) (home, settingsPath string) {
	t.Helper()
	home = t.TempDir()
	settingsPath = filepath.Join(t.TempDir(), "settings.json")
	if original != "" {
		if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	return home, settingsPath
}

type uninstalled struct {
	err       bool
	settings  string
	installed bool
	stdout    string
	homeGone  bool
}

func snapshotUninstall(t *testing.T, home, settingsPath string, err error, stdout string) uninstalled {
	t.Helper()
	out := uninstalled{err: err != nil, stdout: stdout}
	if data, readErr := os.ReadFile(settingsPath); readErr == nil {
		out.settings = string(data)
	}
	if cfg, loadErr := config.Load(home); loadErr == nil {
		out.installed = cfg.Install != nil
	}
	_, statErr := os.Stat(home)
	out.homeGone = os.IsNotExist(statErr)
	return out
}

// The restored object is the original compacted, because config.Save re-indents
// the stored raw value and the original source bytes are gone; every other byte
// of the document is untouched.
func TestUninstallRestoresTheOriginalObject(t *testing.T) {
	t.Parallel()
	original := "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": { \"type\": \"command\", \"command\": \"bash ~/.claude/statusline-command.sh\", \"refreshInterval\": 5 }\n}\n"
	home, settingsPath := installFixture(t, original)
	var stdout bytes.Buffer
	err := uninstall(home, "", false, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{
		settings: "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": {\"type\":\"command\",\"command\":\"bash ~/.claude/statusline-command.sh\",\"refreshInterval\":5}\n}\n",
		stdout:   "restored: " + settingsPath + "\n",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUninstallRemovesTheMemberItAdded(t *testing.T) {
	t.Parallel()
	original := "{\n  \"model\": \"claude-sonnet-5\"\n}\n"
	home, settingsPath := installFixture(t, original)
	var stdout bytes.Buffer
	err := uninstall(home, "", false, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: original, installed: false, stdout: "removed statusLine from " + settingsPath + "\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUninstallLeavesAChangedStatusLineAlone(t *testing.T) {
	t.Parallel()
	home, settingsPath := installFixture(t, `{"statusLine":{"type":"command","command":"cat"}}`)
	changed := `{"statusLine":{"type":"command","command":"jq -r .model.display_name"}}`
	if err := os.WriteFile(settingsPath, []byte(changed), 0o600); err != nil {
		t.Fatalf("change: %v", err)
	}
	var stdout bytes.Buffer
	err := uninstall(home, "", false, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: changed, installed: true, stdout: "warning: statusLine was changed since install; nothing restored\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUninstallWithPurgeDeletesTheHome(t *testing.T) {
	t.Parallel()
	original := `{"statusLine":{"type":"command","command":"cat"}}`
	home, settingsPath := installFixture(t, original)
	var stdout bytes.Buffer
	err := uninstall(home, "", true, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: original, installed: false, homeGone: true, stdout: "restored: " + settingsPath + "\npurged: " + home + "\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// --purge is explicit and unconditional: local data goes even when the status
// line is no longer ours and nothing can be restored.
func TestUninstallPurgesEvenWhenTheStatusLineChanged(t *testing.T) {
	t.Parallel()
	home, settingsPath := installFixture(t, `{"statusLine":{"type":"command","command":"cat"}}`)
	changed := `{"statusLine":{"type":"command","command":"jq -r .model.display_name"}}`
	if err := os.WriteFile(settingsPath, []byte(changed), 0o600); err != nil {
		t.Fatalf("change: %v", err)
	}
	var stdout bytes.Buffer
	err := uninstall(home, "", true, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{
		settings: changed,
		homeGone: true,
		stdout:   "warning: statusLine was changed since install; nothing restored\npurged: " + home + "\n",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A previous run that wrote the settings file but died before saving config.json
// leaves the file already restored; the next run must finish the job.
func TestUninstallClearsTheRecordWhenAlreadyRestored(t *testing.T) {
	t.Parallel()
	original := `{"statusLine":{"type":"command","command":"cat"}}`
	home, settingsPath := installFixture(t, original)
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("restore by hand: %v", err)
	}
	var stdout bytes.Buffer
	err := uninstall(home, "", false, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: original, installed: false, stdout: "already restored: " + settingsPath + "\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUninstallWithoutAnInstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := config.Save(home, testConfig()); err != nil {
		t.Fatalf("save: %v", err)
	}
	err := uninstall(home, "", false, &bytes.Buffer{})
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("got %v, want ErrNotInstalled", err)
	}
}

func TestUninstallWithoutAConfigFile(t *testing.T) {
	t.Parallel()
	err := uninstall(t.TempDir(), "", false, &bytes.Buffer{})
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("got %v, want ErrNotInstalled", err)
	}
}

func TestUninstallRefusesAnInvalidConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	seeded := `{"statusLine":{"type":"command","command":"/opt/gaugewire/bin/gaugewire statusline"}}`
	if err := os.WriteFile(settingsPath, []byte(seeded), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	cfg := testConfig()
	cfg.Node.ID = "not-a-uuid"
	cfg.Install = &config.Install{
		SettingsPath:       settingsPath,
		InstalledCommand:   "/opt/gaugewire/bin/gaugewire statusline",
		OriginalStatusLine: json.RawMessage(`{"type":"command","command":"cat"}`),
	}
	raw, marshalErr := json.Marshal(cfg)
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	if err := os.WriteFile(filepath.Join(home, config.File), raw, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	err := uninstall(home, "", false, &bytes.Buffer{})
	after, _ := os.ReadFile(settingsPath)
	type outcome struct {
		invalid  bool
		settings string
	}
	got := outcome{errors.Is(err, config.ErrInvalid), string(after)}
	want := outcome{true, seeded}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunUninstallResolvesTheSettingsFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	// The temp directory is itself reached through a symlink on macOS; resolving
	// it here keeps the expected path the one uninstall reports.
	dir, dirErr := filepath.EvalSymlinks(t.TempDir())
	if dirErr != nil {
		t.Fatalf("resolve temp dir: %v", dirErr)
	}
	settingsPath := filepath.Join(dir, "settings.json")
	original := `{"statusLine":{"type":"command","command":"cat"}}`
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		t.Skipf("working directory: %v", cwdErr)
	}
	relative, relErr := filepath.Rel(cwd, settingsPath)
	if relErr != nil {
		t.Skipf("relative path: %v", relErr)
	}
	var stdout bytes.Buffer
	err := runUninstall(t.Context(), []string{"--settings", relative}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: original, stdout: "restored: " + settingsPath + "\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
