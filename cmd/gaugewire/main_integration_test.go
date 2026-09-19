//go:build integration

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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

// databoxSinks is the one enabled sink the end-to-end test configures; a test
// that must not spawn a flusher passes "[]" instead.
const databoxSinks = `[ { "id": "databox-main", "type": "databox", "enabled": true, "credentials": { "apiKeyEnv": "DATABOX_API_KEY", "apiKeyFile": "" } } ]`

func integrationHome(t *testing.T, rendererCommand, sinks string) string {
	t.Helper()
	home := t.TempDir()
	body := `{
  "schemaVersion": 1,
  "node":    { "id": "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", "alias": "it-node" },
  "account": { "id": "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "alias": "it-account" },
  "renderer": { "command": ` + strconv.Quote(rendererCommand) + ` },
  "publishing": { "minDeltaPercentage": 1.0, "heartbeatInterval": "30m" },
  "sinks": ` + sinks + `
}`
	if err := os.WriteFile(filepath.Join(home, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return home
}

func statuslineOnce(t *testing.T, binary, home string, payload []byte) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, "statusline")
	cmd.Env = append(os.Environ(), "GAUGEWIRE_HOME="+home)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	return string(out), err
}

func TestStatuslineEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	t.Parallel()
	binary := buildBinary(t, "")
	home := integrationHome(t, "cat", databoxSinks)
	payload, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "statusline", "full.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	out, err := statuslineOnce(t, binary, home, payload)
	if err != nil || out != string(payload) {
		t.Fatalf("stdout %q err %v; want the exact payload", out, err)
	}
	pending, _ := filepath.Glob(filepath.Join(home, "pending", "*.json"))
	if len(pending) != 1 {
		t.Fatalf("pending files %v, want exactly one", pending)
	}
	waitForFlushRuns(t, home, 1)
}

// waitForFlushRuns polls the log until it holds exactly want "flush finished"
// records, one per detached flusher process, so a caller's later cleanup does
// not race a flusher still writing to the home directory.
func waitForFlushRuns(t *testing.T, home string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	count := 0
	for time.Now().Before(deadline) {
		log, _ := os.ReadFile(filepath.Join(home, "logs", "gaugewire.log"))
		count = strings.Count(string(log), "flush finished")
		if count == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the log holds %d flush runs, want %d", count, want)
}

// gaugewire runs the built binary against home and returns its stdout.
func gaugewire(t *testing.T, binary, home string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Env = append(os.Environ(), "GAUGEWIRE_HOME="+home)
	out, err := cmd.Output()
	return string(out), err
}

func TestInstallStatuslineUninstallRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	t.Parallel()
	binary := buildBinary(t, "")
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	original := "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": {\"type\":\"command\",\"command\":\"cat\"}\n}\n"
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := gaugewire(t, binary, home, "install", "--settings", settingsPath, "--node-alias", "it-node"); err != nil {
		t.Fatalf("install: %v", err)
	}
	installed, err := os.ReadFile(settingsPath)
	if err != nil || !strings.Contains(string(installed), binary+" statusline") {
		t.Fatalf("settings after install: %q err %v", installed, err)
	}
	payload, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "statusline", "full.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	out, err := statuslineOnce(t, binary, home, payload)
	if err != nil || out != string(payload) {
		t.Fatalf("statusline: %q err %v", out, err)
	}
	if _, err := gaugewire(t, binary, home, "doctor", "--settings", settingsPath); err != nil {
		t.Fatalf("doctor after install should be healthy: %v", err)
	}
	if _, err := gaugewire(t, binary, home, "uninstall", "--settings", settingsPath); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	restored, err := os.ReadFile(settingsPath)
	if err != nil || string(restored) != original {
		t.Fatalf("settings after uninstall: %q err %v", restored, err)
	}
}

func TestDoctorExitsOneWhenUnhealthy(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "")
	home := integrationHome(t, "", "[]")
	_, err := gaugewire(t, binary, home, "doctor", "--settings", filepath.Join(t.TempDir(), "settings.json"))
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("got %v, want exit code 1", err)
	}
}

func TestParallelStatuslinesPublishOnce(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "")
	// No sink is enabled, so nothing is spooled and no flusher is spawned: the
	// test observes the state lock alone, with nothing left running at cleanup.
	home := integrationHome(t, "", "[]")
	payload, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "statusline", "full.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	const sessions = 8
	var wg sync.WaitGroup
	errs := make(chan error, sessions)
	for range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := statuslineOnce(t, binary, home, payload)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("statusline failed: %v", err)
		}
	}
	log, readErr := os.ReadFile(filepath.Join(home, "logs", "gaugewire.log"))
	if readErr != nil {
		t.Fatalf("read log: %v", readErr)
	}
	pending, _ := filepath.Glob(filepath.Join(home, "pending", "*.json"))
	type outcome struct {
		published int
		pending   int
	}
	got := outcome{published: strings.Count(string(log), "event published"), pending: len(pending)}
	want := outcome{published: 1, pending: 0}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
