package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/store"
)

var installAt = time.Date(2026, 9, 18, 10, 30, 0, 0, time.UTC)

func installOpts(settingsPath string) installOptions {
	return installOptions{
		settingsPath: settingsPath,
		executable:   "/opt/gaugewire/bin/gaugewire",
		now:          func() time.Time { return installAt },
		hostname:     func() (string, error) { return "mac-mini-01", nil },
	}
}

type installed struct {
	err      string
	settings string
	backup   string
	cfg      config.Config
	stdout   string
}

func snapshotInstall(t *testing.T, home, settingsPath string, err error, stdout string) installed {
	t.Helper()
	out := installed{stdout: stdout}
	if err != nil {
		out.err = err.Error()
	}
	if data, readErr := os.ReadFile(settingsPath); readErr == nil {
		out.settings = string(data)
	}
	if data, readErr := os.ReadFile(settingsPath + ".gaugewire-backup-20260918-103000"); readErr == nil {
		out.backup = string(data)
	}
	if cfg, loadErr := config.Load(home); loadErr == nil {
		cfg.Node.ID, cfg.Account.ID = "", "" // generated; asserted separately
		out.cfg = cfg
	}
	return out
}

// compactJSON normalizes a saved raw value. config.Save marshals with
// json.MarshalIndent, which re-indents every json.RawMessage, so the original
// status line comes back JSON-equal to the settings file rather than
// byte-equal; only the backup file keeps the original bytes.
func compactJSON(r json.RawMessage) string {
	var out bytes.Buffer
	if json.Compact(&out, r) != nil {
		return string(r)
	}
	return out.String()
}

func TestInstallSplicesTheStatusLineAndSavesTheOriginal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	original := "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": { \"type\": \"command\", \"command\": \"bash ~/.claude/statusline-command.sh\", \"refreshInterval\": 5 }\n}\n"
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	var stdout bytes.Buffer
	err := install(home, installOpts(settingsPath), &stdout)
	got := snapshotInstall(t, home, settingsPath, err, stdout.String())
	cfg := config.Default()
	cfg.Node = config.Identity{Alias: "mac-mini-01"}
	cfg.Account = config.Identity{Alias: "claude-01"}
	cfg.Renderer = config.Renderer{Command: "bash ~/.claude/statusline-command.sh"}
	cfg.Install = &config.Install{
		SettingsPath:       settingsPath,
		InstalledCommand:   "/opt/gaugewire/bin/gaugewire statusline",
		OriginalStatusLine: json.RawMessage(`{ "type": "command", "command": "bash ~/.claude/statusline-command.sh", "refreshInterval": 5 }`),
	}
	want := installed{
		settings: "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": { \"type\": \"command\", \"command\": \"/opt/gaugewire/bin/gaugewire statusline\", \"refreshInterval\": 5 }\n}\n",
		backup:   original,
		cfg:      cfg,
		stdout: strings.Join([]string{
			"settings:     " + settingsPath,
			"backup:       " + settingsPath + ".gaugewire-backup-20260918-103000",
			"status line:  /opt/gaugewire/bin/gaugewire statusline",
			"renderer:     bash ~/.claude/statusline-command.sh",
		}, "\n") + "\n",
	}
	// node and account lines carry generated ids; compare them by prefix below.
	gotLines := strings.Split(strings.TrimSpace(got.stdout), "\n")
	got.stdout = strings.Join(gotLines[:4], "\n") + "\n"
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(installed{}), cmp.Transformer("raw", compactJSON)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
	if len(gotLines) != 6 || !strings.HasPrefix(gotLines[4], "node:         mac-mini-01 ") || !strings.HasPrefix(gotLines[5], "account:      claude-01 ") {
		t.Fatalf("identity lines: %q", gotLines[4:])
	}
}

func TestInstallCreatesTheSettingsFileWhenMissing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), ".claude", "settings.json")
	var stdout bytes.Buffer
	err := install(home, installOpts(settingsPath), &stdout)
	got := snapshotInstall(t, home, settingsPath, err, stdout.String())
	cfg := config.Default()
	cfg.Node = config.Identity{Alias: "mac-mini-01"}
	cfg.Account = config.Identity{Alias: "claude-01"}
	cfg.Install = &config.Install{SettingsPath: settingsPath, InstalledCommand: "/opt/gaugewire/bin/gaugewire statusline", OriginalStatusLine: nil}
	want := installed{
		settings: "{\n  \"statusLine\": {\"type\":\"command\",\"command\":\"/opt/gaugewire/bin/gaugewire statusline\"}\n}",
		cfg:      cfg,
	}
	got.stdout = ""
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(installed{}), cmp.Transformer("raw", compactJSON)); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

// An object with no members carries no renderer to keep, so install replaces it
// with the fresh object rather than leaving a statusLine without a type.
func TestInstallAddsTypeToAnEmptyStatusLineObject(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := install(home, installOpts(settingsPath), &bytes.Buffer{})
	cfg, loadErr := config.Load(home)
	data, _ := os.ReadFile(settingsPath)
	type outcome struct {
		failed   bool
		loadErr  bool
		file     string
		renderer string
	}
	got := outcome{err != nil, loadErr != nil, string(data), cfg.Renderer.Command}
	want := outcome{false, false, `{"statusLine":{"type":"command","command":"/opt/gaugewire/bin/gaugewire statusline"}}`, ""}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInstallRefusesWhenAlreadyInstalled(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	before, _ := os.ReadFile(settingsPath)
	err := install(home, installOpts(settingsPath), &bytes.Buffer{})
	after, _ := os.ReadFile(settingsPath)
	if !errors.Is(err, ErrAlreadyInstalled) || string(before) != string(after) {
		t.Fatalf("err=%v changed=%v, want ErrAlreadyInstalled and no change", err, string(before) != string(after))
	}
}

func TestInstallWithForceKeepsTheSavedOriginal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"cat"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	opts := installOpts(settingsPath)
	opts.force = true
	opts.executable = "/usr/local/bin/gaugewire"
	err := install(home, opts, &bytes.Buffer{})
	cfg, loadErr := config.Load(home)
	data, _ := os.ReadFile(settingsPath)
	first, _ := os.ReadFile(settingsPath + ".gaugewire-backup-20260918-103000")
	second, _ := os.ReadFile(settingsPath + ".gaugewire-backup-20260918-103000-2")
	type outcome struct {
		err      bool
		loadErr  bool
		renderer string
		original string
		command  string
		file     string
		backup1  string
		backup2  string
	}
	got := outcome{
		err != nil, loadErr != nil, cfg.Renderer.Command, compactJSON(cfg.Install.OriginalStatusLine),
		cfg.Install.InstalledCommand, string(data), string(first), string(second),
	}
	want := outcome{
		false, false, "cat", `{"type":"command","command":"cat"}`,
		"/usr/local/bin/gaugewire statusline",
		`{"statusLine":{"type":"command","command":"/usr/local/bin/gaugewire statusline"}}`,
		`{"statusLine":{"type":"command","command":"cat"}}`,
		`{"statusLine":{"type":"command","command":"/opt/gaugewire/bin/gaugewire statusline"}}`,
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInstallRefusesAReinstallWithoutARecord(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	seeded := `{"statusLine":{"type":"command","command":"/opt/gaugewire/bin/gaugewire statusline"}}`
	if err := os.WriteFile(settingsPath, []byte(seeded), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	opts := installOpts(settingsPath)
	opts.force = true
	err := install(home, opts, &bytes.Buffer{})
	_, loadErr := config.Load(home)
	data, _ := os.ReadFile(settingsPath)
	type outcome struct {
		noRecord bool
		noConfig bool
		file     string
	}
	got := outcome{errors.Is(err, ErrNoInstallRecord), errors.Is(loadErr, config.ErrMissing), string(data)}
	want := outcome{true, true, seeded}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInstallLeavesTheFileAloneWhenStatusLineIsNotAnObject(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		seeded string
	}{
		{"a string", `{"statusLine":"cat"}`},
		{"null", `{"statusLine":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			settingsPath := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(settingsPath, []byte(tc.seeded), 0o600); err != nil {
				t.Fatalf("seed: %v", err)
			}
			err := install(home, installOpts(settingsPath), &bytes.Buffer{})
			data, _ := os.ReadFile(settingsPath)
			_, backupErr := os.Stat(settingsPath + ".gaugewire-backup-20260918-103000")
			type outcome struct {
				failed   bool
				file     string
				noBackup bool
			}
			got := outcome{err != nil, string(data), errors.Is(backupErr, os.ErrNotExist)}
			want := outcome{true, tc.seeded, true}
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestInstallKeepsExistingIdentityAndAliasesOverride(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	existing := testConfig()
	if err := config.Save(home, existing); err != nil {
		t.Fatalf("save: %v", err)
	}
	opts := installOpts(settingsPath)
	opts.nodeAlias = "studio-02"
	if err := install(home, opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	cfg, err := config.Load(home)
	type identity struct{ nodeID, nodeAlias, accountID, accountAlias string }
	got := identity{cfg.Node.ID, cfg.Node.Alias, cfg.Account.ID, cfg.Account.Alias}
	want := identity{existing.Node.ID, "studio-02", existing.Account.ID, existing.Account.Alias}
	if err != nil || got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

func TestInstalledCommandQuotesOnlyPathsWithSpaces(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/usr/local/bin/gaugewire":                 "/usr/local/bin/gaugewire statusline",
		`C:\Program Files\Gaugewire\gaugewire.exe`: `"C:/Program Files/Gaugewire/gaugewire.exe" statusline`,
		`C:\Users\me\gaugewire.exe`:                "C:/Users/me/gaugewire.exe statusline",
	}
	got := map[string]string{}
	for exe := range cases {
		got[exe] = installedCommand(exe)
	}
	if diff := cmp.Diff(cases, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunInstallUsesTheFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	var stdout bytes.Buffer
	err := runInstall(t.Context(), []string{"--settings", settingsPath, "--node-alias", "n1", "--account-alias", "a1"}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	cfg, loadErr := config.Load(home)
	type outcome struct {
		err, loadErr       bool
		nodeAlias, account string
		settingsPath       string
	}
	got := outcome{err != nil, loadErr != nil, cfg.Node.Alias, cfg.Account.Alias, cfg.Install.SettingsPath}
	want := outcome{false, false, "n1", "a1", settingsPath}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
