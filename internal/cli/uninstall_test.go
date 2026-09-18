package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sulcer/gaugewire/internal/config"
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
