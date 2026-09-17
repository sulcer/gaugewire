package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeHonoursTheEnvironmentOverride(t *testing.T) {
	t.Setenv(HomeEnv, "/tmp/gaugewire-override")
	got, err := Home()
	if err != nil || got != "/tmp/gaugewire-override" {
		t.Fatalf("got %q, %v; want /tmp/gaugewire-override, nil", got, err)
	}
}

func TestHomeDefaultsToTheUserConfigDir(t *testing.T) {
	t.Setenv(HomeEnv, "")
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("no user config dir on this machine: %v", err)
	}
	got, err := Home()
	want := filepath.Join(configDir, "gaugewire")
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q, nil", got, err, want)
	}
}

func TestEnsureLayoutCreatesThePrivateDirectories(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "gaugewire")
	if err := EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	got := make(map[string]bool)
	for _, dir := range []string{"", PendingDir, DeadLetterDir, LogsDir} {
		info, err := os.Stat(filepath.Join(home, dir))
		got[dir] = err == nil && info.IsDir()
	}
	want := map[string]bool{"": true, PendingDir: true, DeadLetterDir: true, LogsDir: true}
	if len(got) != len(want) || !got[""] || !got[PendingDir] || !got[DeadLetterDir] || !got[LogsDir] {
		t.Fatalf("directories %v, want %v", got, want)
	}
}

func TestEnsureLayoutIsIdempotent(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "gaugewire")
	first := EnsureLayout(home)
	second := EnsureLayout(home)
	if first != nil || second != nil {
		t.Fatalf("errors %v, %v; want nil, nil", first, second)
	}
}
