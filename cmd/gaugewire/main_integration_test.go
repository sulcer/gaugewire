//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/store"
)

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
	cmd := exec.CommandContext(t.Context(), binary, "version")
	cmd.Env = childEnv()
	out, err := cmd.CombinedOutput()
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
	cmd := exec.CommandContext(t.Context(), binary)
	cmd.Env = childEnv()
	err := cmd.Run()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("got error %v, want an *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code %d, want 2", exitErr.ExitCode())
	}
}

// databoxSinks is the one enabled sink the end-to-end test configures; a test
// that must not spawn a flusher passes "[]" instead. Its base URL is a
// loopback port nothing listens on, so no request can leave the machine.
const databoxSinks = `[ { "id": "databox-main", "type": "databox", "enabled": true, "baseUrl": "http://127.0.0.1:1", "credentials": { "apiKeyEnv": "DATABOX_API_KEY", "apiKeyFile": "" } } ]`

// childEnv is the test process environment without any DATABOX_API_KEY, plus
// extra, so a key in the developer's shell never reaches a child process.
func childEnv(extra ...string) []string {
	parent := os.Environ()
	env := make([]string, 0, len(parent)+len(extra))
	for _, kv := range parent {
		if !strings.HasPrefix(kv, "DATABOX_API_KEY=") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

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

// openPayload moves the fixture's two resets ahead of now: the binary reads the
// real clock, and the reducer refuses a reading whose window has ended.
func openPayload(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "statusline", "full.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	ahead := func(d time.Duration) []byte {
		return []byte(strconv.FormatInt(time.Now().Add(d).Unix(), 10))
	}
	raw = bytes.ReplaceAll(raw, []byte("1789659600"), ahead(time.Hour))
	return bytes.ReplaceAll(raw, []byte("1789714800"), ahead(25*time.Hour))
}

func statuslineOnce(t *testing.T, binary, home string, payload []byte) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, "statusline")
	cmd.Env = childEnv("GAUGEWIRE_HOME=" + home)
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
	payload := openPayload(t)
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

// waitForFlushRuns polls the log for want "flush finished" records, one per
// detached flusher, so cleanup does not race a flusher still writing.
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

func gaugewire(t *testing.T, binary, home string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Env = childEnv("GAUGEWIRE_HOME=" + home)
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
	payload := openPayload(t)
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
	// No sink is enabled, so the test observes the state lock alone.
	home := integrationHome(t, "", "[]")
	payload := openPayload(t)
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

func TestFlushThroughTheBinaryAgainstAFakeAPI(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/datasets/ds-hist/data":
			_, _ = w.Write([]byte(`{"requestId":"r","status":"success","ingestionId":"ing-h","message":"ok"}`))
		case "/v1/datasets/ds-cur/data":
			_, _ = w.Write([]byte(`{"requestId":"r","status":"success","ingestionId":"ing-c","message":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	sinks := `[{"id":"databox-main","type":"databox","enabled":true,"baseUrl":` + strconv.Quote(srv.URL) + `,"accountId":123456,"dataSourceId":4754489,"currentDatasetId":"ds-cur","historyDatasetId":"ds-hist","credentials":{"apiKeyEnv":"GW_IT_DATABOX_KEY","apiKeyFile":""}}]`
	home := integrationHome(t, "", sinks)
	payload := openPayload(t)
	cmd := exec.CommandContext(t.Context(), binary, "statusline")
	cmd.Env = childEnv("GAUGEWIRE_HOME="+home, "GW_IT_DATABOX_KEY=it-key")
	cmd.Stdin = bytes.NewReader(payload)
	if _, err := cmd.Output(); err != nil {
		t.Fatalf("statusline: %v", err)
	}
	waitForFlushRuns(t, home, 1)
	pending, _ := filepath.Glob(filepath.Join(home, "pending", "*.json"))
	raw, err := os.ReadFile(filepath.Join(home, "state.json"))
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	var state store.State
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	record := state.LastIngestion["databox-main"]
	got := struct {
		pending int
		history string
		current string
	}{len(pending), record.History, record.Current}
	want := struct {
		pending int
		history string
		current string
	}{0, "ing-h", "ing-c"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
