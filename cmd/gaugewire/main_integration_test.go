//go:build integration

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// buildBinary compiles the command into a temporary directory and returns its path.
func buildBinary(t *testing.T, ldflags string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "gaugewire")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	args := []string{"build", "-o", binary}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, ".")
	out, err := exec.CommandContext(t.Context(), "go", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return binary
}

func TestBinaryPrintsInjectedVersion(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "-X main.version=v9.9.9 -X main.commit=cafe -X main.date=2026-09-17")
	out, err := exec.CommandContext(t.Context(), binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\n%s", err, out)
	}
	want := "gaugewire v9.9.9\ncommit: cafe\nbuilt:  2026-09-17\n"
	if string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestBinaryExitsTwoOnUsageError(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "")
	err := exec.CommandContext(t.Context(), binary).Run()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("got error %v, want an *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code %d, want 2", exitErr.ExitCode())
	}
}
