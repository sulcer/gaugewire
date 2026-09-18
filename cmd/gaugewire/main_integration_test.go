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

func integrationHome(t *testing.T, rendererCommand string) string {
	t.Helper()
	home := t.TempDir()
	body := `{
  "schemaVersion": 1,
  "node":    { "id": "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", "alias": "it-node" },
  "account": { "id": "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "alias": "it-account" },
  "renderer": { "command": ` + strconv.Quote(rendererCommand) + ` },
  "publishing": { "minDeltaPercentage": 1.0, "heartbeatInterval": "30m" },
  "sinks": [ { "id": "databox-main", "type": "databox", "enabled": true, "credentials": { "apiKeyEnv": "DATABOX_API_KEY", "apiKeyFile": "" } } ]
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
	home := integrationHome(t, "cat")
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
	waitForFlushersToSettle(t, home)
	log, _ := os.ReadFile(filepath.Join(home, "logs", "gaugewire.log"))
	if !strings.Contains(string(log), "flush finished") {
		t.Fatal("the detached flusher never logged a finished run")
	}
}

// waitForFlushersToSettle polls the log for the "delivered " summary line each
// flusher process prints exactly once on exit, and returns once that count has
// stopped growing, so a caller's later cleanup does not race a detached
// flusher that is still writing to the home directory.
func waitForFlushersToSettle(t *testing.T, home string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var history []int
	for time.Now().Before(deadline) {
		log, _ := os.ReadFile(filepath.Join(home, "logs", "gaugewire.log"))
		count := strings.Count(string(log), "delivered ")
		history = append(history, count)
		if len(history) > 3 {
			history = history[len(history)-3:]
		}
		if len(history) == 3 && history[0] >= 1 && history[0] == history[1] && history[1] == history[2] {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the flushers never settled")
}

func TestParallelStatuslinesPublishOnce(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "")
	home := integrationHome(t, "")
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
	pending, _ := filepath.Glob(filepath.Join(home, "pending", "*.json"))
	if len(pending) != 1 {
		t.Fatalf("pending files %d, want exactly one", len(pending))
	}
	waitForFlushersToSettle(t, home)
}
