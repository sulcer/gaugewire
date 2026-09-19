package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type resolved struct {
	path   string
	failed bool
}

func TestResolveSettingsPathFollowsASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symlinks")
	}
	t.Parallel()
	// The temp directory is itself reached through a symlink on macOS, so the
	// expected value is the fully resolved target, not just its absolute path.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	target := filepath.Join(dir, "target.json")
	if writeErr := os.WriteFile(target, []byte(`{}`), 0o600); writeErr != nil {
		t.Fatalf("seed: %v", writeErr)
	}
	link := filepath.Join(dir, "link.json")
	if linkErr := os.Symlink(target, link); linkErr != nil {
		t.Fatalf("symlink: %v", linkErr)
	}
	path, err := resolveSettingsPath(link)
	got := resolved{path, err != nil}
	if want := (resolved{target, false}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveSettingsPathKeepsAMissingFileAbsolute(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), ".claude", "settings.json")
	path, err := resolveSettingsPath(missing)
	got := resolved{path, err != nil}
	if want := (resolved{missing, false}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveSettingsPathResolvesTheParentOfAMissingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "dotfiles-claude")
	link := filepath.Join(base, ".claude")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	path, err := resolveSettingsPath(filepath.Join(link, "settings.json"))
	got := resolved{path, err != nil}
	if want := (resolved{filepath.Join(realTarget, "settings.json"), false}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
