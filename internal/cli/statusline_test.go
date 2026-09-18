package cli

import (
	"bytes"
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
	payload := fixture(t, "full.json")
	got := runHotPath(t, home, cfg, payload)
	want := hotPath{stdout: string(payload), spawned: []string{home}, pending: 1}
	if got.err != want.err || got.stdout != want.stdout || got.stderr != want.stderr || len(got.spawned) != 1 || got.spawned[0] != home || got.pending != want.pending {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestStatuslineWithoutARendererWritesNothing(t *testing.T) {
	home := t.TempDir()
	got := runHotPath(t, home, testConfig(databoxSink()), fixture(t, "full.json"))
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

func TestStatuslineWithoutConfigExitsQuietly(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	var stdout bytes.Buffer
	err := runStatusline(t.Context(), BuildInfo{}, IO{Stdin: bytes.NewReader(fixture(t, "full.json")), Stdout: &stdout, Stderr: &bytes.Buffer{}}, func(string) error { return nil })
	if err != nil || stdout.Len() != 0 {
		t.Fatalf("err=%v stdout=%q; want nil and nothing written", err, stdout.String())
	}
}
