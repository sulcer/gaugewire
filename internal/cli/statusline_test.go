package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/store"
)

type hotPath struct {
	err     bool
	stdout  string
	stderr  string
	spawned []string
	pending int
}

func runHotPath(t *testing.T, home string, cfg config.Config, payload []byte) hotPath {
	t.Helper()
	t.Setenv(store.HomeEnv, home)
	if err := config.Save(home, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	var stdout, stderr bytes.Buffer
	var spawned []string
	spawn := func(h string) error { spawned = append(spawned, h); return nil }
	err := runStatusline(t.Context(), BuildInfo{Version: "1.0.0"}, IO{Stdin: bytes.NewReader(payload), Stdout: &stdout, Stderr: &stderr}, spawn)
	pending, _, _ := store.Counts(home)
	return hotPath{err: err != nil, stdout: stdout.String(), stderr: stderr.String(), spawned: spawned, pending: pending}
}

func TestStatuslineRendersExactBytesAndSpoolsAnEvent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	home := t.TempDir()
	cfg := testConfig(databoxSink())
	cfg.Renderer.Command = "cat"
	payload := openPayload(t, "full.json")
	got := runHotPath(t, home, cfg, payload)
	want := hotPath{stdout: string(payload), spawned: []string{home}, pending: 1}
	if got.err != want.err || got.stdout != want.stdout || got.stderr != want.stderr || len(got.spawned) != 1 || got.spawned[0] != home || got.pending != want.pending {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestStatuslineWithoutARendererWritesNothing(t *testing.T) {
	home := t.TempDir()
	got := runHotPath(t, home, testConfig(databoxSink()), openPayload(t, "full.json"))
	want := hotPath{spawned: []string{home}, pending: 1}
	if got.err != want.err || got.stdout != "" || got.stderr != "" || len(got.spawned) != 1 || got.pending != want.pending {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestStatuslineStillRendersWhenObservationIsSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	home := t.TempDir()
	cfg := testConfig(databoxSink())
	cfg.Renderer.Command = "cat"
	payload := fixture(t, "old-version.json")
	got := runHotPath(t, home, cfg, payload)
	if got.err || got.stdout != string(payload) || len(got.spawned) != 0 || got.pending != 0 {
		t.Fatalf("got %+v, want rendered payload, no spawn, no event", got)
	}
}

func TestStatuslineRendersWhenConfigIsInvalid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	body := `{
  "schemaVersion": 1,
  "node": { "id": "not-a-uuid", "alias": "n" },
  "account": { "id": "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "alias": "a" },
  "renderer": { "command": "cat" },
  "publishing": { "minDeltaPercentage": 1, "heartbeatInterval": "30m" },
  "sinks": []
}`
	if err := os.WriteFile(filepath.Join(home, config.File), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stdout, stderr bytes.Buffer
	var spawned []string
	spawn := func(h string) error { spawned = append(spawned, h); return nil }
	payload := fixture(t, "full.json")
	err := runStatusline(t.Context(), BuildInfo{Version: "1.0.0"}, IO{Stdin: bytes.NewReader(payload), Stdout: &stdout, Stderr: &stderr}, spawn)
	pending, _, _ := store.Counts(home)
	got := hotPath{err: err != nil, stdout: stdout.String(), stderr: stderr.String(), spawned: spawned, pending: pending}
	want := hotPath{err: false, stdout: string(payload), stderr: "", spawned: nil, pending: 0}
	if got.err != want.err || got.stdout != want.stdout || got.stderr != want.stderr || len(got.spawned) != len(want.spawned) || got.pending != want.pending {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestStatuslineWithoutConfigExitsQuietly(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	var stdout bytes.Buffer
	err := runStatusline(t.Context(), BuildInfo{}, IO{Stdin: bytes.NewReader(fixture(t, "full.json")), Stdout: &stdout, Stderr: &bytes.Buffer{}}, func(string) error { return nil })
	entries, readErr := os.ReadDir(home)
	if readErr != nil {
		t.Fatalf("read home: %v", readErr)
	}
	type quietExit struct {
		err     bool
		stdout  string
		entries int
	}
	got := quietExit{err: err != nil, stdout: stdout.String(), entries: len(entries)}
	want := quietExit{}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
