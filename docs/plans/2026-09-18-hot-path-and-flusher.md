# Hot Path and Flusher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `gaugewire statusline` and `gaugewire flush` real: read Claude Code's stdin, keep the user's renderer byte-exact, reduce and spool under the lock, spawn a detached flusher that delivers due events through the sink interface with backoff and dead letters, and add the offline `status` view. No concrete sink yet.

**Architecture:** Four new packages surround the domain and store built in the previous plan: `config` (config.json), `logging` (rotating JSON log), `renderer` (run the saved command with exact stdin) and `sink` (the `Sink` interface, error classes, backoff, and the flusher run). `internal/cli` gains `statusline`, `flush` and `status` subcommands plus the platform-specific detached spawn. Every step of the hot path fails open.

**Tech Stack:** Go 1.27 standard library (`os/exec`, `log/slog`, `flag`, `uuid`, `testing/synctest` not needed), existing deps only (`gofrs/flock`, `go-cmp` in tests).

**Spec:** `docs/spec/gaugewire/hot-path.md`, `docs/spec/gaugewire/spool-and-flush.md`, `docs/spec/gaugewire/data-contract.md` (config.json, spooled event, state.json), `docs/spec/gaugewire/cli-and-install.md` (Commands, status, Logging), `docs/spec/gaugewire/architecture.md`, `docs/spec/gaugewire/testing-strategy.md`.

## Global Constraints

- Branch `feat/hot-path-and-flusher` from `main` (`747755f`). Conventional Commits without scope, first line ≤ 72 characters, `Co-Authored-By: Claude ... <noreply@anthropic.com>` trailer via the heredoc form. `make check` before every commit; the repo's git hooks are active.
- **Commit authority:** the owner approves pushes. Commits on this branch are pre-approved for this plan's execution; never push.
- No new dependencies. Standard library plus the existing `github.com/gofrs/flock` and `github.com/google/go-cmp` (tests only).
- Dependency direction: `config, renderer, logging → store/quota only`; `sink → store, quota`; `cli → everything`. Nothing imports `cli`.
- The hot path writes zero bytes of its own to stdout; renderer stdout and stderr pass through unchanged; no network; lock wait ≤ 1 s and fail open; exit code 0 always.
- Logs are JSON lines in `logs/gaugewire.log`, rotated at 1 MiB; never log raw payloads, paths, session ids or credentials. The renderer command string is a path: never log it.
- Tests: `t.Parallel()` unless the test uses `t.Setenv`; one whole-value assertion per test; expectations from this plan, never from running the code. Tests that spawn the built binary carry `//go:build integration`. Renderer tests that need `cat` or `sh` skip on Windows.
- Files created with the Write tool; Go files gofumpt-formatted (the edit hook does it).
- Third-party behaviour is relied on only when publicly documented; nothing in the repo may mention internal services, repositories, code or tooling of any third party.

## Before you start

```bash
git switch main && git pull --ff-only
git switch -c feat/hot-path-and-flusher
```

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/config/config.go`, `config_test.go` | `Config` and its parts, `Duration`, `Default`, `Load`, `Save`, `Validate`, helpers into `quota` types |
| `internal/logging/logging.go`, `logging_test.go` | `Open` (append, rotate at 1 MiB), `Discard` |
| `internal/renderer/renderer.go`, `shell_unix.go`, `shell_windows.go`, `renderer_test.go` | `Run` the saved command with exact stdin |
| `internal/sink/sink.go`, `backoff.go`, `sink_test.go`, `backoff_test.go` | `Sink`, `Error` classes, `Classify`, `NextAttempt` |
| `internal/sink/flusher.go`, `flusher_test.go` | `Flusher.Run`: lock, quarantine, per-sink delivery, dead letters, `lastFlush` |
| `internal/store/spool.go` (modify), `spool_test.go` (modify) | `Quarantine` |
| `internal/cli/cli.go` (modify), `cli_test.go` (modify) | `IO`, new `Run` signature, usage text |
| `internal/cli/observe.go`, `observe_test.go` | the locked reduce-decide-spool step |
| `internal/cli/spawn.go`, `detach_unix.go`, `detach_windows.go` | detached flusher spawn |
| `internal/cli/statusline.go`, `statusline_test.go` | the hot path command |
| `internal/cli/sinks.go`, `flush.go`, `flush_test.go` | sink construction from config, the flush command |
| `internal/cli/status.go`, `status_test.go`, `testdata/status_*.golden` | the offline status view |
| `cmd/gaugewire/main.go` (modify), `main_integration_test.go` (modify) | wire `Run`; end-to-end tests |

---

### Task 1: Configuration

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Consumes: `store.WriteFileAtomic`, `quota.Publishing`, `quota.Identity`.
- Produces: `config.File` ("config.json"); `config.SchemaVersion` (1); `config.SinkTypeDatabox` ("databox"); `config.DefaultAPIKeyEnv` ("DATABOX_API_KEY"); `config.ErrMissing`; `config.Duration` (text-marshalled `time.Duration`); `config.Identity{ID, Alias}`; `config.Renderer{Command}`; `config.Install{SettingsPath, InstalledCommand string; OriginalStatusLine json.RawMessage}`; `config.Publishing{MinDeltaPercentage float64; HeartbeatInterval Duration}`; `config.Credentials{APIKeyEnv, APIKeyFile}`; `config.Sink{ID, Type string; Enabled bool; BaseURL string; AccountID, DataSourceID int64; CurrentDatasetID, HistoryDatasetID string; Credentials}`; `config.Config{SchemaVersion int; Node, Account Identity; Renderer; Install *Install; Publishing; Sinks []Sink}`; `config.Default() Config`; `config.Load(home string) (Config, error)`; `config.Save(home string, c Config) error`; `(Config).Validate() error`; `(Config).EnabledSinkIDs() []string`; `(Config).QuotaPublishing() quota.Publishing`; `(Config).QuotaIdentity(platform, observerVersion string) quota.Identity`.

- [ ] **Step 1: Write the failing tests**

`internal/config/config_test.go`:

```go
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/quota"
)

const specExample = `{
  "schemaVersion": 1,
  "node":    { "id": "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", "alias": "mac-mini-01" },
  "account": { "id": "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "alias": "claude-01" },
  "renderer": { "command": "bash ~/.claude/statusline-command.sh" },
  "install": {
    "settingsPath": "/Users/me/.claude/settings.json",
    "installedCommand": "/usr/local/bin/gaugewire statusline",
    "originalStatusLine": { "type": "command", "command": "bash ~/.claude/statusline-command.sh", "refreshInterval": 5 }
  },
  "publishing": { "minDeltaPercentage": 1.0, "heartbeatInterval": "30m" },
  "sinks": [
    {
      "id": "databox-main", "type": "databox", "enabled": true,
      "baseUrl": "https://api.databox.com",
      "accountId": 123456, "dataSourceId": 4754489,
      "currentDatasetId": "uuid", "historyDatasetId": "uuid",
      "credentials": { "apiKeyEnv": "DATABOX_API_KEY", "apiKeyFile": "" }
    }
  ]
}`

func specConfig() Config {
	return Config{
		SchemaVersion: 1,
		Node:          Identity{ID: "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", Alias: "mac-mini-01"},
		Account:       Identity{ID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", Alias: "claude-01"},
		Renderer:      Renderer{Command: "bash ~/.claude/statusline-command.sh"},
		Install: &Install{
			SettingsPath:       "/Users/me/.claude/settings.json",
			InstalledCommand:   "/usr/local/bin/gaugewire statusline",
			OriginalStatusLine: json.RawMessage(`{ "type": "command", "command": "bash ~/.claude/statusline-command.sh", "refreshInterval": 5 }`),
		},
		Publishing: Publishing{MinDeltaPercentage: 1.0, HeartbeatInterval: Duration(30 * time.Minute)},
		Sinks: []Sink{{
			ID: "databox-main", Type: "databox", Enabled: true,
			BaseURL: "https://api.databox.com", AccountID: 123456, DataSourceID: 4754489,
			CurrentDatasetID: "uuid", HistoryDatasetID: "uuid",
			Credentials: Credentials{APIKeyEnv: "DATABOX_API_KEY"},
		}},
	}
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, File), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestLoadParsesTheSpecExample(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeConfig(t, home, specExample)
	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(specConfig(), got, cmp.Transformer("raw", func(r json.RawMessage) string { return string(r) })); diff != "" {
		t.Fatalf("config mismatch (-want +got):\n%s", diff)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	want := specConfig()
	if err := Save(home, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(want, got, cmp.Transformer("raw", func(r json.RawMessage) string {
		var v any
		_ = json.Unmarshal(r, &v)
		b, _ := json.Marshal(v)
		return string(b)
	})); diff != "" {
		t.Fatalf("config mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadReportsAMissingFile(t *testing.T) {
	t.Parallel()
	_, err := Load(t.TempDir())
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("got %v, want ErrMissing", err)
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeConfig(t, home, `{"schemaVersion": `)
	_, err := Load(home)
	if err == nil || errors.Is(err, ErrMissing) {
		t.Fatalf("got %v, want a decode error", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	valid := specConfig
	cases := []struct {
		name   string
		mutate func(c *Config)
		wantOK bool
	}{
		{"spec example is valid", func(*Config) {}, true},
		{"default without sinks is valid once identities are set", func(c *Config) { *c = Default(); c.Node = Identity{ID: "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", Alias: "n"}; c.Account = Identity{ID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", Alias: "a"} }, true},
		{"wrong schema version", func(c *Config) { c.SchemaVersion = 2 }, false},
		{"node id is not a uuid", func(c *Config) { c.Node.ID = "mac-mini" }, false},
		{"account alias is empty", func(c *Config) { c.Account.Alias = "" }, false},
		{"zero threshold", func(c *Config) { c.Publishing.MinDeltaPercentage = 0 }, false},
		{"zero heartbeat", func(c *Config) { c.Publishing.HeartbeatInterval = 0 }, false},
		{"unknown sink type", func(c *Config) { c.Sinks[0].Type = "carrier-pigeon" }, false},
		{"duplicate sink ids", func(c *Config) { c.Sinks = append(c.Sinks, c.Sinks[0]) }, false},
		{"empty sink id", func(c *Config) { c.Sinks[0].ID = "" }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := valid()
			tc.mutate(&c)
			if got := c.Validate() == nil; got != tc.wantOK {
				t.Fatalf("valid=%v, want %v (err: %v)", got, tc.wantOK, c.Validate())
			}
		})
	}
}

func TestDurationTextRoundTrip(t *testing.T) {
	t.Parallel()
	var got struct {
		D Duration `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"d":"30m"}`), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got.D != Duration(30*time.Minute) || string(out) != `{"d":"30m0s"}` {
		t.Fatalf("got %v and %s, want 30m and {\"d\":\"30m0s\"}", time.Duration(got.D), out)
	}
}

func TestHelpersProjectIntoQuotaTypes(t *testing.T) {
	t.Parallel()
	c := specConfig()
	c.Sinks = append(c.Sinks, Sink{ID: "databox-spare", Type: "databox", Enabled: false})
	type projection struct {
		IDs        []string
		Publishing quota.Publishing
		Identity   quota.Identity
	}
	got := projection{IDs: c.EnabledSinkIDs(), Publishing: c.QuotaPublishing(), Identity: c.QuotaIdentity("darwin", "1.0.0")}
	want := projection{
		IDs:        []string{"databox-main"},
		Publishing: quota.Publishing{MinDeltaPercentage: 1.0, HeartbeatInterval: 30 * time.Minute},
		Identity: quota.Identity{
			NodeID: "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", NodeAlias: "mac-mini-01", Platform: "darwin",
			AccountID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", AccountAlias: "claude-01", ObserverVersion: "1.0.0",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("projection mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/config/`
Expected: FAIL to build, `undefined: Config`, `undefined: Load`, `undefined: Save`, `undefined: Default`, `undefined: Duration`, `undefined: File`, `undefined: ErrMissing`.

- [ ] **Step 3: Write `internal/config/config.go`**

```go
// Package config loads, validates and saves config.json in the home directory.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"uuid"

	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

// File is the configuration file name inside the home directory.
const File = "config.json"

// SchemaVersion is the version of config.json; a breaking change bumps it.
const SchemaVersion = 1

// SinkTypeDatabox is the only sink type v1 knows.
const SinkTypeDatabox = "databox"

// DefaultAPIKeyEnv is the environment variable a sink reads its key from when
// no key file is configured.
const DefaultAPIKeyEnv = "DATABOX_API_KEY"

// ErrMissing means config.json does not exist; install creates it.
var ErrMissing = errors.New("config.json not found; run gaugewire install first")

// Duration is a time.Duration that reads and writes as a Go duration string.
type Duration time.Duration

// MarshalText renders the duration as time.Duration.String does.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

// UnmarshalText parses a Go duration string.
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("duration %q: %w", text, err)
	}
	*d = Duration(parsed)
	return nil
}

// Identity is a configured node or account identity; it is never inferred.
type Identity struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

// Renderer is the user's previous status-line command. Empty means none.
type Renderer struct {
	Command string `json:"command"`
}

// Install remembers what install changed so uninstall can restore it exactly.
type Install struct {
	SettingsPath       string          `json:"settingsPath"`
	InstalledCommand   string          `json:"installedCommand"`
	OriginalStatusLine json.RawMessage `json:"originalStatusLine"`
}

// Publishing holds the dedupe thresholds.
type Publishing struct {
	MinDeltaPercentage float64  `json:"minDeltaPercentage"`
	HeartbeatInterval  Duration `json:"heartbeatInterval"`
}

// Credentials say where a sink reads its API key: the file if set, else the
// environment variable. The key itself is never stored here.
type Credentials struct {
	APIKeyEnv  string `json:"apiKeyEnv"`
	APIKeyFile string `json:"apiKeyFile"`
}

// Sink configures one destination.
type Sink struct {
	ID               string      `json:"id"`
	Type             string      `json:"type"`
	Enabled          bool        `json:"enabled"`
	BaseURL          string      `json:"baseUrl,omitempty"`
	AccountID        int64       `json:"accountId,omitempty"`
	DataSourceID     int64       `json:"dataSourceId,omitempty"`
	CurrentDatasetID string      `json:"currentDatasetId,omitempty"`
	HistoryDatasetID string      `json:"historyDatasetId,omitempty"`
	Credentials      Credentials `json:"credentials"`
}

// Config is the whole config.json.
type Config struct {
	SchemaVersion int        `json:"schemaVersion"`
	Node          Identity   `json:"node"`
	Account       Identity   `json:"account"`
	Renderer      Renderer   `json:"renderer"`
	Install       *Install   `json:"install,omitempty"`
	Publishing    Publishing `json:"publishing"`
	Sinks         []Sink     `json:"sinks"`
}

// Default returns a configuration with the spec defaults and no identities or sinks.
func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Publishing:    Publishing{MinDeltaPercentage: 1.0, HeartbeatInterval: Duration(30 * time.Minute)},
		Sinks:         []Sink{},
	}
}

// Load reads and validates config.json.
func Load(home string) (Config, error) {
	raw, err := os.ReadFile(filepath.Join(home, File))
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrMissing
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", File, err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", File, err)
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", File, err)
	}
	return c, nil
}

// Save validates and writes config.json atomically with owner-only permissions.
func Save(home string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return store.WriteFileAtomic(filepath.Join(home, File), data, 0o600)
}

// Validate checks the invariants every command relies on.
func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion %d is not %d", c.SchemaVersion, SchemaVersion)
	}
	if _, err := uuid.Parse(c.Node.ID); err != nil {
		return fmt.Errorf("node.id must be a UUID: %w", err)
	}
	if _, err := uuid.Parse(c.Account.ID); err != nil {
		return fmt.Errorf("account.id must be a UUID: %w", err)
	}
	if c.Node.Alias == "" || c.Account.Alias == "" {
		return errors.New("node.alias and account.alias must be set")
	}
	if c.Publishing.MinDeltaPercentage <= 0 {
		return errors.New("publishing.minDeltaPercentage must be positive")
	}
	if c.Publishing.HeartbeatInterval <= 0 {
		return errors.New("publishing.heartbeatInterval must be positive")
	}
	seen := make(map[string]bool, len(c.Sinks))
	for _, s := range c.Sinks {
		if s.ID == "" {
			return errors.New("every sink needs an id")
		}
		if seen[s.ID] {
			return fmt.Errorf("sink id %q is used twice", s.ID)
		}
		seen[s.ID] = true
		if s.Type != SinkTypeDatabox {
			return fmt.Errorf("sink %q has unknown type %q", s.ID, s.Type)
		}
	}
	return nil
}

// EnabledSinkIDs lists the ids of enabled sinks in configuration order.
func (c Config) EnabledSinkIDs() []string {
	ids := make([]string, 0, len(c.Sinks))
	for _, s := range c.Sinks {
		if s.Enabled {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// QuotaPublishing converts the thresholds into the domain's type.
func (c Config) QuotaPublishing() quota.Publishing {
	return quota.Publishing{MinDeltaPercentage: c.Publishing.MinDeltaPercentage, HeartbeatInterval: time.Duration(c.Publishing.HeartbeatInterval)}
}

// QuotaIdentity builds the snapshot identity for this machine.
func (c Config) QuotaIdentity(platform, observerVersion string) quota.Identity {
	return quota.Identity{
		NodeID: c.Node.ID, NodeAlias: c.Node.Alias, Platform: platform,
		AccountID: c.Account.ID, AccountAlias: c.Account.Alias, ObserverVersion: observerVersion,
	}
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/config/` then `make check`.
Expected: `ok`, lint clean. The `uuid` import is Go 1.27's standard package.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "$(cat <<'EOF'
feat: add config.json loading, validation and saving

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Logging

**Files:**
- Create: `internal/logging/logging.go`, `internal/logging/logging_test.go`

**Interfaces:**
- Consumes: `store.LogsDir`.
- Produces: `logging.FileName` ("gaugewire.log"); `logging.Open(home string) (*slog.Logger, func() error, error)`; `logging.Discard() *slog.Logger`.

- [ ] **Step 1: Write the failing tests**

`internal/logging/logging_test.go`:

```go
package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sulcer/gaugewire/internal/store"
)

func TestOpenWritesJSONLinesToTheLogFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	logger, closeLog, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	logger.Info("event published", "eventId", "evt-1")
	if err := closeLog(); err != nil {
		t.Fatalf("close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, store.LogsDir, FileName))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line); err != nil {
		t.Fatalf("log line is not JSON: %v: %q", err, raw)
	}
	got := map[string]any{"msg": line["msg"], "eventId": line["eventId"], "level": line["level"]}
	want := map[string]any{"msg": "event published", "eventId": "evt-1", "level": "INFO"}
	if got["msg"] != want["msg"] || got["eventId"] != want["eventId"] || got["level"] != want["level"] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOpenRotatesAFullLogFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, store.LogsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	full := make([]byte, rotateAt)
	if err := os.WriteFile(filepath.Join(dir, FileName), full, 0o600); err != nil {
		t.Fatalf("seed full log: %v", err)
	}
	logger, closeLog, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	logger.Info("after rotation")
	_ = closeLog()
	current, _ := os.ReadFile(filepath.Join(dir, FileName))
	rotated, _ := os.Stat(filepath.Join(dir, FileName+".1"))
	if rotated == nil || rotated.Size() != int64(rotateAt) || !strings.Contains(string(current), "after rotation") || len(current) >= rotateAt {
		t.Fatalf("rotation failed: current=%d bytes, rotated=%v", len(current), rotated)
	}
}

func TestDiscardIsDisabledAtEveryLevel(t *testing.T) {
	t.Parallel()
	if Discard().Handler().Enabled(t.Context(), slog.LevelError) {
		t.Fatal("discard logger must not be enabled")
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/logging/`
Expected: FAIL to build, `undefined: Open`, `undefined: FileName`, `undefined: rotateAt`, `undefined: Discard`.

- [ ] **Step 3: Write `internal/logging/logging.go`**

```go
// Package logging opens Gaugewire's rotating JSON log inside the home directory.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/sulcer/gaugewire/internal/store"
)

// FileName is the log file inside logs/.
const FileName = "gaugewire.log"

const rotateAt = 1 << 20

// Open returns a JSON logger appending to logs/gaugewire.log, rotating the
// previous file to .1 when it has reached one MiB, plus a function that closes
// the file.
func Open(home string) (*slog.Logger, func() error, error) {
	dir := filepath.Join(home, store.LogsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName)
	if info, err := os.Stat(path); err == nil && info.Size() >= rotateAt {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, nil, fmt.Errorf("rotate log: %w", err)
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log: %w", err)
	}
	return slog.New(slog.NewJSONHandler(file, nil)), file.Close, nil
}

// Discard returns a logger that drops everything, for paths with no home directory.
func Discard() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
```

`slog.DiscardHandler` exists since Go 1.24.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/logging/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/logging
git commit -m "$(cat <<'EOF'
feat: add the rotating json log

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Renderer passthrough

**Files:**
- Create: `internal/renderer/renderer.go`, `internal/renderer/shell_unix.go`, `internal/renderer/shell_windows.go`, `internal/renderer/renderer_test.go`

**Interfaces:**
- Produces: `renderer.Run(ctx context.Context, command string, stdin []byte, stdout, stderr io.Writer) error`. An empty command writes nothing and returns nil.

- [ ] **Step 1: Write the failing tests**

`internal/renderer/renderer_test.go`:

```go
package renderer

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"
)

func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh and cat")
	}
}

type outcome struct {
	err    bool
	stdout string
	stderr string
}

func run(t *testing.T, command string, stdin []byte) outcome {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(t.Context(), command, stdin, &stdout, &stderr)
	return outcome{err: err != nil, stdout: stdout.String(), stderr: stderr.String()}
}

func TestRunPassesStdinThroughByteForByte(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	payload := []byte("{\"version\":\"2.1.274\"}\x1b[31m no newline")
	got := run(t, "cat", payload)
	want := outcome{stdout: string(payload)}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunForwardsStderrAndReportsFailure(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	got := run(t, "printf boom >&2; exit 3", nil)
	want := outcome{err: true, stderr: "boom"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunWithoutACommandWritesNothing(t *testing.T) {
	t.Parallel()
	got := run(t, "   ", []byte("payload"))
	want := outcome{}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := Run(ctx, "sleep 5", nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("err=%v after %s; want an error well before 5 s", err, time.Since(started))
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/renderer/`
Expected: FAIL to build, `undefined: Run`.

- [ ] **Step 3: Write the implementation**

`internal/renderer/renderer.go`:

```go
// Package renderer runs the user's previous status-line command with the exact
// stdin Claude Code sent and forwards its output unchanged.
package renderer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// Run executes command through the platform shell with stdin as its input,
// streaming stdout and stderr to the given writers. An empty command is a
// no-op. The child shares this process's group, so cancellation reaches it.
func Run(ctx context.Context, command string, stdin []byte, stdout, stderr io.Writer) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	name, args := shellCommand(command)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("renderer: %w", err)
	}
	return nil
}
```

`internal/renderer/shell_unix.go`:

```go
//go:build !windows

package renderer

func shellCommand(command string) (string, []string) {
	return "/bin/sh", []string{"-c", command}
}
```

`internal/renderer/shell_windows.go`:

```go
//go:build windows

package renderer

import "os/exec"

// shellCommand mirrors Claude Code on Windows: Git Bash when bash is on PATH,
// PowerShell otherwise.
func shellCommand(command string) (string, []string) {
	if bash, err := exec.LookPath("bash"); err == nil {
		return bash, []string{"-c", command}
	}
	return "powershell", []string{"-NoProfile", "-Command", command}
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/renderer/` then `GOOS=windows go vet ./internal/renderer/` (cross-vet the Windows file) then `make check`.
Expected: `ok`, both vets clean, lint clean. If gosec flags G204 (subprocess with variable) on `exec.CommandContext`, add `//nolint:gosec // G204: the command is the user's own configured status line` on that line.

- [ ] **Step 5: Commit**

```bash
git add internal/renderer
git commit -m "$(cat <<'EOF'
feat: run the existing status-line renderer with exact stdin

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Sink interface, error classes and backoff

**Files:**
- Create: `internal/sink/sink.go`, `internal/sink/backoff.go`, `internal/sink/sink_test.go`, `internal/sink/backoff_test.go`

**Interfaces:**
- Consumes: `quota.Snapshot`.
- Produces: `sink.Sink` interface (`ID() string`, `PublishBatch(ctx, []quota.Snapshot) error`); `sink.MaxBatch` (100); `sink.Class` with `sink.Retryable` and `sink.Permanent`; `sink.Error{Class, Code string, Err error}` implementing `error` and `Unwrap`; `sink.NewRetryable(code string, err error) error`; `sink.NewPermanent(code string, err error) error`; `sink.Classify(err error) (Class, string)` (unknown errors are retryable); `sink.NextAttempt(attempts int, now time.Time, random func() float64) time.Time`.

- [ ] **Step 1: Write the failing tests**

`internal/sink/sink_test.go`:

```go
package sink

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	type classified struct {
		class Class
		code  string
	}
	base := errors.New("boom")
	cases := []struct {
		name string
		err  error
		want classified
	}{
		{"permanent keeps its code", NewPermanent("invalid_api_key", base), classified{Permanent, "invalid_api_key"}},
		{"retryable keeps its code", NewRetryable("rate_limited", base), classified{Retryable, "rate_limited"}},
		{"wrapped sink error is still classified", fmt.Errorf("sink x: %w", NewPermanent("forbidden", base)), classified{Permanent, "forbidden"}},
		{"unknown error is retryable without a code", base, classified{Retryable, ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			class, code := Classify(tc.err)
			got := classified{class, code}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSinkErrorUnwrapsAndPrints(t *testing.T) {
	t.Parallel()
	base := errors.New("401 unauthorized")
	err := NewPermanent("invalid_api_key", base)
	if !errors.Is(err, base) || err.Error() != "permanent (invalid_api_key): 401 unauthorized" {
		t.Fatalf("got %q, Is(base)=%v", err.Error(), errors.Is(err, base))
	}
}
```

`internal/sink/backoff_test.go`:

```go
package sink

import (
	"testing"
	"time"
)

func TestNextAttemptFollowsTheSchedule(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	noJitter := func() float64 { return 0.5 }
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, 5 * time.Second},
		{2, 30 * time.Second},
		{3, 2 * time.Minute},
		{4, 10 * time.Minute},
		{5, 30 * time.Minute},
		{9, 30 * time.Minute},
		{0, 5 * time.Second},
	}
	for _, tc := range cases {
		got := NextAttempt(tc.attempts, now, noJitter).Sub(now)
		if got != tc.want {
			t.Fatalf("attempt %d: got %s, want %s", tc.attempts, got, tc.want)
		}
	}
}

func TestNextAttemptJitterStaysWithinTwentyPercent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	low := NextAttempt(2, now, func() float64 { return 0 }).Sub(now)
	high := NextAttempt(2, now, func() float64 { return 0.999999 }).Sub(now)
	if low != 24*time.Second || high < 35*time.Second || high >= 36*time.Second {
		t.Fatalf("low=%s high=%s; want 24s and just under 36s", low, high)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/sink/`
Expected: FAIL to build, `undefined: Class`, `undefined: NewPermanent`, `undefined: Classify`, `undefined: NextAttempt`.

- [ ] **Step 3: Write the implementation**

`internal/sink/sink.go`:

```go
// Package sink defines the destination interface, the error classes a sink
// reports, the retry schedule, and the flusher that delivers spooled events.
package sink

import (
	"context"
	"errors"

	"github.com/sulcer/gaugewire/internal/quota"
)

// MaxBatch is the largest number of snapshots handed to a sink in one call.
const MaxBatch = 100

// Sink is a destination for snapshots. Implementations classify failures with
// NewRetryable and NewPermanent so the flusher can decide what to do.
type Sink interface {
	ID() string
	PublishBatch(ctx context.Context, snapshots []quota.Snapshot) error
}

// Class says whether a failed delivery should be retried or dead-lettered.
type Class int

const (
	// Retryable failures are transient: network, timeouts, 5xx, throttling.
	Retryable Class = iota
	// Permanent failures need a human: credentials, schema, configuration.
	Permanent
)

// Error is a classified delivery failure.
type Error struct {
	Class Class
	Code  string
	Err   error
}

func (e *Error) Error() string {
	class := "retryable"
	if e.Class == Permanent {
		class = "permanent"
	}
	if e.Code == "" {
		return class + ": " + e.Err.Error()
	}
	return class + " (" + e.Code + "): " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// NewRetryable wraps err as a transient failure with an optional code.
func NewRetryable(code string, err error) error {
	return &Error{Class: Retryable, Code: code, Err: err}
}

// NewPermanent wraps err as a failure that must not be retried.
func NewPermanent(code string, err error) error {
	return &Error{Class: Permanent, Code: code, Err: err}
}

// Classify returns the class and code of a delivery error. An error a sink did
// not classify is treated as retryable, so nothing is ever dropped by accident.
func Classify(err error) (Class, string) {
	if sinkErr, ok := errors.AsType[*Error](err); ok {
		return sinkErr.Class, sinkErr.Code
	}
	return Retryable, ""
}
```

`internal/sink/backoff.go`:

```go
package sink

import "time"

// schedule is the retry backoff by attempt number; the last entry repeats forever.
var schedule = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

const jitterFraction = 0.2

// NextAttempt returns when a delivery should be retried after the given number
// of failed attempts (1 for the first failure), with ±20 % jitter drawn from
// random, which must return a value in [0, 1).
func NextAttempt(attempts int, now time.Time, random func() float64) time.Time {
	index := attempts - 1
	if index < 0 {
		index = 0
	}
	if index >= len(schedule) {
		index = len(schedule) - 1
	}
	base := schedule[index]
	jitter := time.Duration((random()*2 - 1) * jitterFraction * float64(base))
	return now.Add(base + jitter)
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/sink/` then `make check`.
Expected: `ok`, lint clean (drop the `fmt` placeholder if flagged).

- [ ] **Step 5: Commit**

```bash
git add internal/sink
git commit -m "$(cat <<'EOF'
feat: add the sink interface, error classes and backoff

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Quarantine and the flusher

**Files:**
- Modify: `internal/store/spool.go`, `internal/store/spool_test.go`
- Create: `internal/sink/flusher.go`, `internal/sink/flusher_test.go`

**Interfaces:**
- Consumes: `store.TryLock`, `store.ListPending`, `store.UpdatePending`, `store.DeletePending`, `store.DeadLetter`, `store.Lock`, `store.LoadState`, `store.SaveState`, `store.FlushRecord`; `sink.Classify`, `sink.NextAttempt`, `sink.MaxBatch`.
- Produces: `store.Quarantine(home string) ([]string, error)` (renames undecodable `pending/*.json` to `dead-letter/<name>.unreadable`, returns the names moved); `sink.FlushLockFile` ("flush.lock"); `sink.RequestTimeout` (15 s); `sink.RunTimeout` (2 min); `sink.Result{Delivered, Retried, DeadLettered, Quarantined int; Skipped bool}`; `sink.Flusher{Home string; Sinks []Sink; Now func() time.Time; Random func() float64; Logger *slog.Logger}`; `(Flusher).Run(ctx) (Result, error)`.

- [ ] **Step 1: Write the failing store test**

Append to `internal/store/spool_test.go`:

```go
func TestQuarantineMovesUndecodablePendingFilesAside(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	if _, err := WritePending(home, event("evt-good", captured)); err != nil {
		t.Fatalf("write good: %v", err)
	}
	bad := filepath.Join(home, PendingDir, "1789657100000-evt-bad.json")
	if err := os.WriteFile(bad, []byte(`{"eventType": "cha`), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	moved, err := Quarantine(home)
	if err != nil {
		t.Fatalf("Quarantine: %v", err)
	}
	pending, listErr := ListPending(home)
	_, statErr := os.Stat(filepath.Join(home, DeadLetterDir, "1789657100000-evt-bad.json.unreadable"))
	type outcome struct {
		moved      []string
		pendingIDs []string
		listOK     bool
		quarantined bool
	}
	got := outcome{moved: moved, listOK: listErr == nil, quarantined: statErr == nil}
	for _, pe := range pending {
		got.pendingIDs = append(got.pendingIDs, pe.Event.Snapshot.EventID)
	}
	want := outcome{moved: []string{"1789657100000-evt-bad.json"}, pendingIDs: []string{"evt-good"}, listOK: true, quarantined: true}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Add `Quarantine` to `internal/store/spool.go`**

```go
// Quarantine renames every pending file that cannot be decoded to
// dead-letter/<name>.unreadable, so a corrupt file never blocks delivery of the
// others. It returns the file names it moved. The rename keeps the bytes for a
// human to inspect; nothing can be added to content that does not decode.
func Quarantine(home string) ([]string, error) {
	pendingDir := filepath.Join(home, PendingDir)
	names, err := eventFiles(pendingDir)
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, name := range names {
		path := filepath.Join(pendingDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return moved, fmt.Errorf("read %s: %w", path, err)
		}
		var ev Event
		if json.Unmarshal(raw, &ev) == nil {
			continue
		}
		target := filepath.Join(home, DeadLetterDir, name+".unreadable")
		if err := os.Rename(path, target); err != nil {
			return moved, fmt.Errorf("quarantine %s: %w", path, err)
		}
		moved = append(moved, name)
	}
	return moved, nil
}
```

Run: `go test -race -count=1 ./internal/store/` → `ok`.

- [ ] **Step 3: Write the failing flusher tests**

`internal/sink/flusher_test.go`:

```go
package sink

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

// fakeSink returns the scripted errors in order, then nil, and records every batch.
type fakeSink struct {
	id      string
	script  []error
	batches [][]string
}

func (f *fakeSink) ID() string { return f.id }

func (f *fakeSink) PublishBatch(_ context.Context, snapshots []quota.Snapshot) error {
	ids := make([]string, 0, len(snapshots))
	for _, s := range snapshots {
		ids = append(ids, s.EventID)
	}
	f.batches = append(f.batches, ids)
	if len(f.script) == 0 {
		return nil
	}
	err := f.script[0]
	f.script = f.script[1:]
	return err
}

var now = time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)

func flusherHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	return home
}

func spool(t *testing.T, home, id string, at time.Time, sinks ...string) {
	t.Helper()
	ev := store.Event{EventType: quota.EventChange, Snapshot: quota.Snapshot{SchemaVersion: 1, EventID: id, CapturedAt: at}, Delivery: map[string]store.DeliveryState{}}
	for _, s := range sinks {
		ev.Delivery[s] = store.DeliveryState{NextAttemptAt: at}
	}
	if _, err := store.WritePending(home, ev); err != nil {
		t.Fatalf("spool %s: %v", id, err)
	}
}

func run(t *testing.T, home string, sinks ...Sink) (Result, error) {
	t.Helper()
	f := Flusher{Home: home, Sinks: sinks, Now: func() time.Time { return now }, Random: func() float64 { return 0.5 }, Logger: logging.Discard()}
	return f.Run(t.Context())
}

type spoolState struct {
	result   Result
	runErr   bool
	pending  map[string]map[string]store.DeliveryState
	dead     []string
	batches  [][]string
	lastFlush *store.FlushRecord
}

func snapshotSpool(t *testing.T, home string, res Result, err error, sinks ...*fakeSink) spoolState {
	t.Helper()
	st := spoolState{result: res, runErr: err != nil, pending: map[string]map[string]store.DeliveryState{}}
	pending, listErr := store.ListPending(home)
	if listErr != nil {
		t.Fatalf("ListPending: %v", listErr)
	}
	for _, pe := range pending {
		st.pending[pe.Event.Snapshot.EventID] = pe.Event.Delivery
	}
	entries, _ := os.ReadDir(filepath.Join(home, store.DeadLetterDir))
	for _, e := range entries {
		st.dead = append(st.dead, e.Name())
	}
	sort.Strings(st.dead)
	for _, s := range sinks {
		st.batches = append(st.batches, s.batches...)
	}
	state, _ := store.LoadState(home)
	st.lastFlush = state.LastFlush
	return st
}

func TestRunDeliversDueEventsAndDeletesThem(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-2*time.Minute), "a")
	spool(t, home, "evt-2", now.Add(-time.Minute), "a")
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	want := spoolState{
		result:    Result{Delivered: 2},
		pending:   map[string]map[string]store.DeliveryState{},
		batches:   [][]string{{"evt-1", "evt-2"}},
		lastFlush: &store.FlushRecord{At: now, OK: true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunRetriesWithBackoffAndStopsTheSink(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-2*time.Minute), "a")
	spool(t, home, "evt-2", now.Add(-time.Minute), "a")
	a := &fakeSink{id: "a", script: []error{NewRetryable("rate_limited", errors.New("429"))}}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	retried := store.DeliveryState{Attempts: 1, NextAttemptAt: now.Add(5 * time.Second), LastError: "retryable (rate_limited): 429", LastErrorCode: "rate_limited"}
	want := spoolState{
		result:    Result{Retried: 2},
		runErr:    true,
		pending:   map[string]map[string]store.DeliveryState{"evt-1": {"a": retried}, "evt-2": {"a": retried}},
		batches:   [][]string{{"evt-1", "evt-2"}},
		lastFlush: &store.FlushRecord{At: now, OK: false, Error: "sink a: retryable (rate_limited): 429"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunDeadLettersPermanentFailuresAndContinues(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-2*time.Minute), "a")
	a := &fakeSink{id: "a", script: []error{NewPermanent("invalid_api_key", errors.New("401"))}}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	want := spoolState{
		result:    Result{DeadLettered: 1},
		pending:   map[string]map[string]store.DeliveryState{},
		dead:      []string{"1789657080000-evt-1.json"},
		batches:   [][]string{{"evt-1"}},
		lastFlush: &store.FlushRecord{At: now, OK: true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunSkipsEventsThatAreNotDueOrNotTargeted(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-future", now.Add(time.Hour), "a")
	spool(t, home, "evt-other", now.Add(-time.Minute), "b")
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	got := snapshotSpool(t, home, res, err, a)
	want := spoolState{
		result: Result{},
		pending: map[string]map[string]store.DeliveryState{
			"evt-future": {"a": {NextAttemptAt: now.Add(time.Hour)}},
			"evt-other":  {"b": {NextAttemptAt: now.Add(-time.Minute)}},
		},
		lastFlush: &store.FlushRecord{At: now, OK: true},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunKeepsAnEventUntilEverySinkAccepted(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-time.Minute), "a", "b")
	a := &fakeSink{id: "a"}
	b := &fakeSink{id: "b", script: []error{NewRetryable("", errors.New("timeout"))}}
	res, err := run(t, home, a, b)
	got := snapshotSpool(t, home, res, err, a, b)
	want := spoolState{
		result:  Result{Delivered: 1, Retried: 1},
		runErr:  true,
		pending: map[string]map[string]store.DeliveryState{"evt-1": {"b": {Attempts: 1, NextAttemptAt: now.Add(5 * time.Second), LastError: "retryable: timeout"}}},
		batches: [][]string{{"evt-1"}, {"evt-1"}},
		lastFlush: &store.FlushRecord{At: now, OK: false, Error: "sink b: retryable: timeout"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(spoolState{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunChunksAtMaxBatch(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	for i := range MaxBatch + 1 {
		spool(t, home, fmt.Sprintf("evt-%03d", i), now.Add(time.Duration(i-200)*time.Second), "a")
	}
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	sizes := []int{}
	for _, b := range a.batches {
		sizes = append(sizes, len(b))
	}
	if err != nil || res.Delivered != MaxBatch+1 || len(sizes) != 2 || sizes[0] != MaxBatch || sizes[1] != 1 {
		t.Fatalf("delivered=%d sizes=%v err=%v; want %d in two calls of %d and 1", res.Delivered, sizes, err, MaxBatch+1, MaxBatch)
	}
}

func TestRunExitsWhenAnotherFlusherHoldsTheLock(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-time.Minute), "a")
	unlock, held, err := store.TryLock(filepath.Join(home, FlushLockFile))
	if err != nil || !held {
		t.Fatalf("pre-lock: held=%v err=%v", held, err)
	}
	defer func() { _ = unlock() }()
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	if err != nil || !res.Skipped || len(a.batches) != 0 {
		t.Fatalf("res=%+v err=%v batches=%v; want Skipped and no calls", res, err, a.batches)
	}
}

func TestRunQuarantinesAnUnreadableFileFirst(t *testing.T) {
	t.Parallel()
	home := flusherHome(t)
	spool(t, home, "evt-1", now.Add(-time.Minute), "a")
	if err := os.WriteFile(filepath.Join(home, store.PendingDir, "1789657000000-evt-bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	a := &fakeSink{id: "a"}
	res, err := run(t, home, a)
	if err != nil || res.Quarantined != 1 || res.Delivered != 1 {
		t.Fatalf("res=%+v err=%v; want 1 quarantined and 1 delivered", res, err)
	}
}
```

The dead-letter file name in `TestRunDeadLettersPermanentFailuresAndContinues` is `now − 2 min = 2026-09-17T14:58:00Z = 1789657080000` milliseconds.

- [ ] **Step 4: Run the tests and watch them fail**

Run: `go test ./internal/sink/`
Expected: FAIL to build, `undefined: Flusher`, `undefined: Result`, `undefined: FlushLockFile`.

- [ ] **Step 5: Write `internal/sink/flusher.go`**

```go
package sink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

// FlushLockFile guarantees one flusher per machine.
const FlushLockFile = "flush.lock"

// RequestTimeout bounds one PublishBatch call.
const RequestTimeout = 15 * time.Second

// RunTimeout bounds one flusher run.
const RunTimeout = 2 * time.Minute

const stateLockFile = "state.lock"

// Result summarises one flusher run.
type Result struct {
	Delivered    int
	Retried      int
	DeadLettered int
	Quarantined  int
	Skipped      bool
}

// Flusher delivers due pending events to every sink, in chronological order,
// chunked, with backoff on transient failures and dead letters on permanent ones.
type Flusher struct {
	Home   string
	Sinks  []Sink
	Now    func() time.Time
	Random func() float64
	Logger *slog.Logger
}

// Run performs one pass and exits. It never sleeps until the next attempt; a
// later status-line invocation relaunches the flusher when work is due.
func (f Flusher) Run(ctx context.Context) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	unlock, held, err := store.TryLock(filepath.Join(f.Home, FlushLockFile))
	if err != nil {
		return Result{}, err
	}
	if !held {
		f.Logger.Info("flush skipped", "reason", "another flusher is running")
		return Result{Skipped: true}, nil
	}
	defer func() { _ = unlock() }()

	var res Result
	moved, err := store.Quarantine(f.Home)
	res.Quarantined = len(moved)
	for _, name := range moved {
		f.Logger.Warn("pending event quarantined", "file", name)
	}
	if err != nil {
		return res, err
	}
	events, err := store.ListPending(f.Home)
	if err != nil {
		return res, err
	}
	now := f.Now().UTC()
	var runErr error
	for _, s := range f.Sinks {
		if err := f.deliver(ctx, s, events, now, &res); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	f.recordFlush(ctx, now, runErr)
	f.Logger.Info("flush finished", "delivered", res.Delivered, "retried", res.Retried, "deadLettered", res.DeadLettered, "quarantined", res.Quarantined)
	return res, runErr
}

func (f Flusher) deliver(ctx context.Context, s Sink, events []store.PendingEvent, now time.Time, res *Result) error {
	due := make([]int, 0, len(events))
	for i, pe := range events {
		d, targeted := pe.Event.Delivery[s.ID()]
		if targeted && !d.NextAttemptAt.After(now) {
			due = append(due, i)
		}
	}
	for start := 0; start < len(due); start += MaxBatch {
		chunk := due[start:min(start+MaxBatch, len(due))]
		snapshots := make([]quota.Snapshot, 0, len(chunk))
		for _, i := range chunk {
			snapshots = append(snapshots, events[i].Event.Snapshot)
		}
		callCtx, cancel := context.WithTimeout(ctx, RequestTimeout)
		err := s.PublishBatch(callCtx, snapshots)
		cancel()
		if err == nil {
			if err := f.acknowledge(s.ID(), events, chunk); err != nil {
				return err
			}
			res.Delivered += len(chunk)
			continue
		}
		class, code := Classify(err)
		if class == Permanent {
			for _, i := range chunk {
				if err := store.DeadLetter(f.Home, events[i], s.ID()+": "+err.Error(), now); err != nil {
					return err
				}
				res.DeadLettered++
				f.Logger.Error("event dead-lettered", "eventId", events[i].Event.Snapshot.EventID, "sink", s.ID(), "code", code)
			}
			continue
		}
		for _, i := range chunk {
			d := events[i].Event.Delivery[s.ID()]
			d.Attempts++
			d.NextAttemptAt = NextAttempt(d.Attempts, now, f.Random)
			d.LastError = err.Error()
			d.LastErrorCode = code
			events[i].Event.Delivery[s.ID()] = d
			if err := store.UpdatePending(events[i]); err != nil {
				return err
			}
			res.Retried++
		}
		f.Logger.Warn("delivery deferred", "sink", s.ID(), "events", len(chunk), "attempt", events[chunk[0]].Event.Delivery[s.ID()].Attempts, "code", code)
		return fmt.Errorf("sink %s: %w", s.ID(), err)
	}
	return nil
}

func (f Flusher) acknowledge(sinkID string, events []store.PendingEvent, chunk []int) error {
	for _, i := range chunk {
		delete(events[i].Event.Delivery, sinkID)
		if len(events[i].Event.Delivery) == 0 {
			if err := store.DeletePending(events[i]); err != nil {
				return err
			}
			continue
		}
		if err := store.UpdatePending(events[i]); err != nil {
			return err
		}
	}
	return nil
}

func (f Flusher) recordFlush(ctx context.Context, now time.Time, runErr error) {
	unlock, err := store.Lock(ctx, filepath.Join(f.Home, stateLockFile), time.Second)
	if err != nil {
		f.Logger.Warn("flush record skipped", "reason", err.Error())
		return
	}
	defer func() { _ = unlock() }()
	state, err := store.LoadState(f.Home)
	if err != nil && !errors.Is(err, store.ErrStateCorrupt) {
		f.Logger.Warn("flush record skipped", "reason", err.Error())
		return
	}
	record := &store.FlushRecord{At: now, OK: runErr == nil}
	if runErr != nil {
		record.Error = runErr.Error()
	}
	state.LastFlush = record
	if err := store.SaveState(f.Home, state); err != nil {
		f.Logger.Warn("flush record not saved", "reason", err.Error())
	}
}
```

`min` is a builtin since Go 1.21.

- [ ] **Step 6: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/sink/ ./internal/store/` then `make check`.
Expected: `ok`, lint clean. If `errors.Join` output format makes the `lastFlush.Error` assertion differ (a single joined error prints as itself), keep the test; if two sinks fail in one run the message is two lines, which no test asserts.

- [ ] **Step 7: Commit**

```bash
git add internal/store internal/sink
git commit -m "$(cat <<'EOF'
feat: deliver spooled events with backoff and dead letters

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: The observe step and the detached spawn

**Files:**
- Modify: `internal/cli/cli.go`, `internal/cli/cli_test.go`, `internal/cli/version.go` (signature only if needed), `cmd/gaugewire/main.go`
- Create: `internal/cli/observe.go`, `internal/cli/observe_test.go`, `internal/cli/spawn.go`, `internal/cli/detach_unix.go`, `internal/cli/detach_windows.go`

**Interfaces:**
- Consumes: `config.Config`, `claude.Parse`, `store.*`, `quota.*`, `logging.Discard`, `uuid.NewV4`.
- Produces: `cli.IO{Stdin io.Reader; Stdout, Stderr io.Writer}`; `cli.Run(ctx context.Context, args []string, info BuildInfo, streams IO) error` (replaces the old signature); `cli.ErrUsage` listing `statusline`, `flush [--requeue]`, `status`, `version`; unexported `observe(ctx, home string, cfg config.Config, payload []byte, now time.Time, info BuildInfo, logger *slog.Logger) observeResult` with `observeResult{Published bool; EventID string; Spawn bool}`; unexported `spawnFlusher(home string) error`; unexported `hasDueWork(home string, now time.Time) bool`.

- [ ] **Step 1: Change `Run` and update its tests**

`internal/cli/cli.go` becomes:

```go
// Package cli implements the gaugewire subcommands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// BuildInfo describes the running binary. The linker sets these values for a
// release build; a development build falls back to the module build info.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// IO carries the process streams so commands never touch os.Stdin or os.Stdout directly.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ErrUsage is returned when the arguments do not name a valid command.
var ErrUsage = errors.New("usage: gaugewire <command>\n\ncommands:\n  statusline          Claude Code status-line adapter (reads stdin)\n  flush [--requeue]   deliver pending events once\n  status              show quota state and spool counts\n  version             print version, commit and build date")

// Run executes the command named by args.
func Run(ctx context.Context, args []string, info BuildInfo, streams IO) error {
	if len(args) == 0 {
		return ErrUsage
	}
	switch args[0] {
	case "version":
		return runVersion(info, streams.Stdout)
	case "statusline":
		return runStatusline(ctx, info, streams, spawnFlusher)
	case "flush":
		return runFlush(ctx, args[1:], info, streams)
	case "status":
		return runStatus(streams.Stdout, time.Now(), time.Local)
	default:
		return fmt.Errorf("unknown command %q: %w", args[0], ErrUsage)
	}
}
```

`runStatusline`, `runFlush` and `runStatus` arrive in Tasks 7 and 8; until then keep the `switch` at `version` plus `default` (with `"time"` imported only once `status` lands) and add the cases when each task lands. In `internal/cli/cli_test.go`, change `runCommand` to call `Run(t.Context(), args, info, IO{Stdout: &stdout})` and keep the three tests unchanged otherwise. In `cmd/gaugewire/main.go` replace the body of `main` with:

```go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	info := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	err := cli.Run(ctx, os.Args[1:], info, cli.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, cli.ErrUsage) {
		os.Exit(2)
	}
	os.Exit(1)
}
```

adding `"context"` and `"os/signal"` to its imports. Run `go test -race -count=1 ./...` and `make check`: green before continuing.

- [ ] **Step 2: Write the failing observe tests**

`internal/cli/observe_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/store"
)

var observedAt = time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "statusline", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func testConfig(sinks ...config.Sink) config.Config {
	c := config.Default()
	c.Node = config.Identity{ID: "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", Alias: "mac-mini-01"}
	c.Account = config.Identity{ID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", Alias: "claude-01"}
	c.Sinks = sinks
	return c
}

func databoxSink() config.Sink {
	return config.Sink{ID: "databox-main", Type: config.SinkTypeDatabox, Enabled: true}
}

type observed struct {
	result   observeResult
	pending  int
	dead     int
	statusOK bool
}

func snapshotObserve(t *testing.T, home string, res observeResult) observed {
	t.Helper()
	pending, dead, err := store.Counts(home)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	state, err := store.LoadState(home)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return observed{result: res, pending: pending, dead: dead, statusOK: state.LastObservedAt != nil}
}

func TestObservePublishesTheFirstObservationAndSpools(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := observe(t.Context(), home, testConfig(databoxSink()), fixture(t, "full.json"), observedAt, BuildInfo{Version: "1.0.0"}, logging.Discard())
	got := snapshotObserve(t, home, res)
	got.result.EventID = ""
	want := observed{result: observeResult{Published: true, Spawn: true}, pending: 1, statusOK: true}
	if got != want || res.EventID == "" {
		t.Fatalf("got %+v (eventId %q), want %+v with a non-empty eventId", got, res.EventID, want)
	}
}

func TestObserveDoesNotPublishTheSameStateTwice(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := testConfig(databoxSink())
	first := observe(t.Context(), home, cfg, fixture(t, "full.json"), observedAt, BuildInfo{}, logging.Discard())
	second := observe(t.Context(), home, cfg, fixture(t, "full.json"), observedAt.Add(time.Minute), BuildInfo{}, logging.Discard())
	got := snapshotObserve(t, home, second)
	got.result.EventID = ""
	want := observed{result: observeResult{Published: false, Spawn: true}, pending: 1, statusOK: true}
	if !first.Published || got != want {
		t.Fatalf("first=%+v second=%+v, want first published and second only spawning for due work", first, got)
	}
}

func TestObserveWithoutSinksPublishesButSpoolsNothing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := observe(t.Context(), home, testConfig(), fixture(t, "full.json"), observedAt, BuildInfo{}, logging.Discard())
	got := snapshotObserve(t, home, res)
	got.result.EventID = ""
	want := observed{result: observeResult{Published: true, Spawn: false}, pending: 0, statusOK: true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestObserveSkipsAnUnsupportedVersion(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	res := observe(t.Context(), home, testConfig(databoxSink()), fixture(t, "old-version.json"), observedAt, BuildInfo{}, logging.Discard())
	got := snapshotObserve(t, home, res)
	want := observed{result: observeResult{}, statusOK: false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestObserveFailsOpenWhenTheLockIsHeld(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	unlock, err := store.Lock(t.Context(), filepath.Join(home, stateLockFile), time.Second)
	if err != nil {
		t.Fatalf("pre-lock: %v", err)
	}
	defer func() { _ = unlock() }()
	res := observe(t.Context(), home, testConfig(databoxSink()), fixture(t, "full.json"), observedAt, BuildInfo{}, logging.Discard())
	got := snapshotObserve(t, home, res)
	want := observed{result: observeResult{}, statusOK: false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestObserveUnderConcurrencyPublishesOnce(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfg := testConfig(databoxSink())
	payload := fixture(t, "full.json")
	const sessions = 20
	var wg sync.WaitGroup
	results := make([]observeResult, sessions)
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = observe(t.Context(), home, cfg, payload, observedAt, BuildInfo{}, logging.Discard())
		}()
	}
	wg.Wait()
	published := 0
	for _, r := range results {
		if r.Published {
			published++
		}
	}
	pending, _, err := store.Counts(home)
	if err != nil || published != 1 || pending != 1 {
		t.Fatalf("published=%d pending=%d err=%v; want exactly one of each", published, pending, err)
	}
}
```

- [ ] **Step 3: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL to build, `undefined: observe`, `undefined: observeResult`, `undefined: stateLockFile`.

- [ ] **Step 4: Write `internal/cli/observe.go`**

```go
package cli

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"time"
	"uuid"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/source/claude"
	"github.com/sulcer/gaugewire/internal/store"
)

const stateLockFile = "state.lock"

const lockWait = time.Second

type observeResult struct {
	Published bool
	EventID   string
	Spawn     bool
}

// observe is the locked part of the hot path: parse, reduce, decide, spool,
// persist. Every failure is logged and swallowed; the renderer must never wait.
func observe(ctx context.Context, home string, cfg config.Config, payload []byte, now time.Time, info BuildInfo, logger *slog.Logger) observeResult {
	obs, issues, err := claude.Parse(bytes.NewReader(payload), now)
	if err != nil {
		logger.Info("observation skipped", "reason", err.Error())
		return observeResult{}
	}
	for _, issue := range issues {
		logger.Warn("window ignored", "path", issue)
	}
	if err := store.EnsureLayout(home); err != nil {
		logger.Error("home directory unavailable", "error", err.Error())
		return observeResult{}
	}
	unlock, err := store.Lock(ctx, filepath.Join(home, stateLockFile), lockWait)
	if err != nil {
		logger.Warn("observation dropped", "reason", err.Error())
		return observeResult{}
	}
	res, targets := reduceAndSpool(home, cfg, obs, now, info, logger)
	if err := unlock(); err != nil {
		logger.Warn("unlock failed", "error", err.Error())
	}
	if !res.Spawn && len(targets) > 0 {
		res.Spawn = hasDueWork(home, now)
	}
	return res
}

func reduceAndSpool(home string, cfg config.Config, obs quota.Observation, now time.Time, info BuildInfo, logger *slog.Logger) (observeResult, []string) {
	state, err := store.LoadState(home)
	if err != nil {
		logger.Warn("state reset", "reason", err.Error())
	}
	state.State = quota.Reduce(state.State, obs)
	decision := quota.Decide(state.State, now, cfg.QuotaPublishing())
	targets := cfg.EnabledSinkIDs()
	var res observeResult
	if decision.Publish {
		eventID := uuid.NewV4().String()
		snapshot := quota.NewSnapshot(cfg.QuotaIdentity(runtime.GOOS, info.Version), state.State, eventID, obs.CapturedAt)
		if len(targets) > 0 {
			ev := store.Event{EventType: decision.EventType, Snapshot: snapshot, Delivery: make(map[string]store.DeliveryState, len(targets))}
			for _, id := range targets {
				ev.Delivery[id] = store.DeliveryState{NextAttemptAt: now.UTC()}
			}
			if _, err := store.WritePending(home, ev); err != nil {
				logger.Error("event not spooled", "eventId", eventID, "error", err.Error())
				return res, targets
			}
			res.Spawn = true
		}
		state.State = quota.MarkPublished(state.State, eventID, snapshot.CapturedAt)
		res.Published = true
		res.EventID = eventID
		logger.Info("event published", "eventId", eventID, "eventType", string(decision.EventType), "targets", len(targets))
	}
	if err := store.SaveState(home, state); err != nil {
		logger.Error("state not saved", "error", err.Error())
	}
	return res, targets
}

// hasDueWork reports whether any pending event is due. A listing error means a
// file needs quarantining, which is the flusher's job, so it counts as due.
func hasDueWork(home string, now time.Time) bool {
	events, err := store.ListPending(home)
	if err != nil {
		return true
	}
	for _, pe := range events {
		for _, d := range pe.Event.Delivery {
			if !d.NextAttemptAt.After(now) {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 5: Write the spawn files**

`internal/cli/spawn.go`:

```go
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/store"
)

// spawnFlusher starts `gaugewire flush` detached from this process: its own
// session, stdin from the null device, stdout and stderr appended to the log
// file, so Claude Code's stdout pipe closes as soon as the hot path exits.
func spawnFlusher(home string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	logPath := filepath.Join(home, store.LogsDir, logging.FileName)
	out, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open log for flusher: %w", err)
	}
	defer func() { _ = out.Close() }()
	cmd := exec.Command(exe, "flush")
	cmd.Env = append(os.Environ(), store.HomeEnv+"="+home)
	cmd.Stdin = nil
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.SysProcAttr = detachAttrs()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start flusher: %w", err)
	}
	return cmd.Process.Release()
}
```

`internal/cli/detach_unix.go`:

```go
//go:build !windows

package cli

import "syscall"

func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
```

`internal/cli/detach_windows.go`:

```go
//go:build windows

package cli

import "syscall"

const detachedProcess = 0x00000008

func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess}
}
```

- [ ] **Step 6: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/cli/` then `GOOS=windows go vet ./internal/cli/` then `make check`.
Expected: `ok`, both vets clean, lint clean. gosec G204 on `exec.Command(exe, "flush")`: add `//nolint:gosec // G204: exe is this binary's own path` if it fires. `spawnFlusher` is unreferenced until Task 7; if `unused` flags it, add `var _ = spawnFlusher` temporarily and remove it in Task 7.

- [ ] **Step 7: Commit**

```bash
git add cmd internal/cli
git commit -m "$(cat <<'EOF'
feat: reduce, decide and spool observations under the state lock

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: The statusline and flush commands, end to end

**Files:**
- Create: `internal/cli/statusline.go`, `internal/cli/statusline_test.go`, `internal/cli/sinks.go`, `internal/cli/flush.go`, `internal/cli/flush_test.go`
- Modify: `internal/cli/cli.go` (add the two cases), `cmd/gaugewire/main_integration_test.go`

**Interfaces:**
- Consumes: `observe`, `spawnFlusher`, `renderer.Run`, `logging.Open`, `config.Load`, `sink.Flusher`, `store.Requeue`.
- Produces: unexported `runStatusline(ctx, info BuildInfo, streams IO, spawn func(home string) error) error`; `runFlush(ctx, args []string, info BuildInfo, streams IO) error`; `buildSinks(cfg config.Config, logger *slog.Logger) []sink.Sink` (returns nothing buildable in this plan; the Databox sink registers itself here later).

- [ ] **Step 1: Write the failing statusline tests**

`internal/cli/statusline_test.go`:

```go
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
```

`internal/cli/flush_test.go`:

```go
package cli

import (
	"bytes"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

func TestFlushRequeuesDeadLettersWhenAsked(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	if err := config.Save(home, testConfig(databoxSink())); err != nil {
		t.Fatalf("save config: %v", err)
	}
	at := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	ev := store.Event{EventType: quota.EventChange, Snapshot: quota.Snapshot{SchemaVersion: 1, EventID: "evt-1", CapturedAt: at}, Delivery: map[string]store.DeliveryState{"databox-main": {NextAttemptAt: at}}}
	if _, err := store.WritePending(home, ev); err != nil {
		t.Fatalf("spool: %v", err)
	}
	list, _ := store.ListPending(home)
	if err := store.DeadLetter(home, list[0], "http 401", at); err != nil {
		t.Fatalf("dead-letter: %v", err)
	}
	var stdout bytes.Buffer
	err := runFlush(t.Context(), []string{"--requeue"}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	pending, dead, _ := store.Counts(home)
	if err != nil || pending != 1 || dead != 0 || stdout.String() != "requeued 1 events\ndelivered 0, retried 0, dead-lettered 0, quarantined 0\n" {
		t.Fatalf("err=%v pending=%d dead=%d stdout=%q", err, pending, dead, stdout.String())
	}
}

func TestFlushWithoutConfigFails(t *testing.T) {
	t.Setenv(store.HomeEnv, t.TempDir())
	err := runFlush(t.Context(), nil, BuildInfo{}, IO{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("got nil, want an error for a missing config")
	}
}
```

In the requeue test the event stays pending after the flush because no sink type is buildable yet, so nothing delivers; `buildSinks` logs a warning per enabled sink and returns none.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL to build, `undefined: runStatusline`, `undefined: runFlush`.

- [ ] **Step 3: Write the implementation**

`internal/cli/statusline.go`:

```go
package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/renderer"
	"github.com/sulcer/gaugewire/internal/store"
)

// runStatusline is the hot path. It reads stdin once, starts the renderer with
// the exact bytes, observes in parallel, spawns the flusher when work is due,
// then forwards the renderer's result. It always returns nil: Claude Code must
// never see a failed status line because of observability.
func runStatusline(ctx context.Context, info BuildInfo, streams IO, spawn func(home string) error) error {
	payload, err := io.ReadAll(streams.Stdin)
	if err != nil {
		return nil
	}
	home, err := store.Home()
	if err != nil {
		return nil
	}
	logger, closeLog := openLogger(home)
	defer closeLog()
	cfg, err := config.Load(home)
	if err != nil {
		if !errors.Is(err, config.ErrMissing) {
			logger.Error("config unusable", "error", err.Error())
		}
		return nil
	}
	rendered := make(chan error, 1)
	go func() {
		rendered <- renderer.Run(ctx, cfg.Renderer.Command, payload, streams.Stdout, streams.Stderr)
	}()
	res := observe(ctx, home, cfg, payload, time.Now(), info, logger)
	if res.Spawn {
		if err := spawn(home); err != nil {
			logger.Error("flusher not started", "error", err.Error())
		}
	}
	if err := <-rendered; err != nil {
		logger.Warn("renderer failed", "error", err.Error())
	}
	return nil
}

func openLogger(home string) (*slog.Logger, func()) {
	logger, closeLog, err := logging.Open(home)
	if err != nil {
		return logging.Discard(), func() {}
	}
	return logger, func() { _ = closeLog() }
}
```

`internal/cli/sinks.go`:

```go
package cli

import (
	"log/slog"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/sink"
)

// buildSinks turns the enabled sink configurations into sinks. A type this
// build cannot construct is logged and skipped; its events stay spooled.
func buildSinks(cfg config.Config, logger *slog.Logger) []sink.Sink {
	sinks := make([]sink.Sink, 0, len(cfg.Sinks))
	for _, s := range cfg.Sinks {
		if !s.Enabled {
			continue
		}
		logger.Warn("sink type not available in this build", "sink", s.ID, "type", s.Type)
	}
	return sinks
}
```

`internal/cli/flush.go`:

```go
package cli

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/sink"
	"github.com/sulcer/gaugewire/internal/store"
)

// runFlush performs one flusher run and prints a one-line summary.
func runFlush(ctx context.Context, args []string, _ BuildInfo, streams IO) error {
	flags := flag.NewFlagSet("flush", flag.ContinueOnError)
	flags.SetOutput(streams.Stderr)
	requeue := flags.Bool("requeue", false, "move dead-letter events back to pending first")
	if err := flags.Parse(args); err != nil {
		return err
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	if err := store.EnsureLayout(home); err != nil {
		return err
	}
	logger, closeLog := openLogger(home)
	defer closeLog()
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	if *requeue {
		moved, err := store.Requeue(home, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(streams.Stdout, "requeued %d events\n", moved)
	}
	flusher := sink.Flusher{Home: home, Sinks: buildSinks(cfg, logger), Now: time.Now, Random: rand.Float64, Logger: logger}
	res, runErr := flusher.Run(ctx)
	fmt.Fprintf(streams.Stdout, "delivered %d, retried %d, dead-lettered %d, quarantined %d\n", res.Delivered, res.Retried, res.DeadLettered, res.Quarantined)
	return runErr
}
```

`math/rand/v2` is standard library; `rand.Float64` is fine for jitter (not security). If gosec flags G404, add `//nolint:gosec // G404: jitter, not security`.

Add the `statusline` and `flush` cases to the `switch` in `cli.go` as shown in Task 6, and delete any temporary `var _ = spawnFlusher` / `errNoSpawner` placeholders.

- [ ] **Step 4: Run the unit tests**

Run: `go test -race -count=1 ./internal/cli/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 5: Add the integration tests**

Append to `cmd/gaugewire/main_integration_test.go` (same build tag and package; reuse `buildBinary`):

```go
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
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		log, _ := os.ReadFile(filepath.Join(home, "logs", "gaugewire.log"))
		if strings.Contains(string(log), "flush finished") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the detached flusher never logged a finished run")
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
}
```

Add `"bytes"`, `"strconv"`, `"strings"`, `"sync"`, `"time"` to that file's imports.

- [ ] **Step 6: Run the integration tests**

Run: `go test -race -tags integration -count=1 ./cmd/gaugewire/` then `make check`.
Expected: `ok`. If the end-to-end test times out on the log line, run the binary by hand with `GAUGEWIRE_HOME=<tmp> ./gaugewire statusline < fixtures/statusline/full.json` and inspect `<tmp>/logs/gaugewire.log`; the flusher writes "flush finished" even with no buildable sinks.

- [ ] **Step 7: Commit**

```bash
git add cmd internal/cli
git commit -m "$(cat <<'EOF'
feat: add the statusline hot path and the flush command

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: The status command

**Files:**
- Create: `internal/cli/status.go`, `internal/cli/status_test.go`, `internal/cli/testdata/status_observed.golden`, `internal/cli/testdata/status_fresh.golden`
- Modify: `internal/cli/cli.go` (add the case and the two clock helpers)

**Interfaces:**
- Produces: unexported `runStatus(stdout io.Writer, now time.Time, zone *time.Location) error`; `renderStatus(cfg config.Config, state store.State, pending, dead int, now time.Time, zone *time.Location) string`.

- [ ] **Step 1: Write the golden files and the failing tests**

`internal/cli/testdata/status_observed.golden` (exact bytes, trailing newline):

```
Gaugewire

Node:     mac-mini-01
Account:  claude-01

5h:       24%        Reset:  18:20
7d:       53.5%      Reset:  Sep 18 09:00

Last observation:  2m ago
Last publish:      2m ago

Pending events:    1
Dead letters:      0

databox-main:      last flush ok 1m ago
```

`internal/cli/testdata/status_fresh.golden`:

```
Gaugewire

Node:     mac-mini-01
Account:  claude-01

5h:       unknown
7d:       unknown

Last observation:  never
Last publish:      never

Pending events:    0
Dead letters:      0

databox-main:      never flushed
```

`internal/cli/status_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return string(data)
}

func TestRenderStatusObserved(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 9, 17, 16, 32, 0, 0, time.UTC) // 18:32 CEST
	captured := now.Add(-2 * time.Minute)
	used5, used7 := 24.0, 53.5
	reset5 := time.Date(2026, 9, 17, 16, 20, 0, 0, time.UTC) // 18:20 CEST, today
	reset7 := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)   // Sep 18 09:00 CEST
	state := store.NewState()
	state.Windows = quota.Windows{
		FiveHour: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used5, ResetsAt: &reset5},
		SevenDay: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used7, ResetsAt: &reset7},
	}
	state.LastObservedAt = &captured
	state.LastPublished = &quota.Published{EventID: "e", CapturedAt: captured, Windows: state.Windows}
	state.LastFlush = &store.FlushRecord{At: now.Add(-time.Minute), OK: true}
	got := renderStatus(testConfig(databoxSink()), state, 1, 0, now, zone)
	if want := golden(t, "status_observed.golden"); got != want {
		t.Fatalf("status mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderStatusFresh(t *testing.T) {
	t.Parallel()
	got := renderStatus(testConfig(databoxSink()), store.NewState(), 0, 0, time.Date(2026, 9, 17, 16, 32, 0, 0, time.UTC), time.UTC)
	if want := golden(t, "status_fresh.golden"); got != want {
		t.Fatalf("status mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL to build, `undefined: renderStatus`.

- [ ] **Step 3: Write `internal/cli/status.go`**

```go
package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

// runStatus prints the offline view: state.json plus spool counts. No network.
func runStatus(stdout io.Writer, now time.Time, zone *time.Location) error {
	home, err := store.Home()
	if err != nil {
		return err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	state, err := store.LoadState(home)
	if err != nil {
		return err
	}
	pending, dead, err := store.Counts(home)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, renderStatus(cfg, state, pending, dead, now, zone))
	return err
}

func renderStatus(cfg config.Config, state store.State, pending, dead int, now time.Time, zone *time.Location) string {
	var b strings.Builder
	b.WriteString("Gaugewire\n\n")
	fmt.Fprintf(&b, "Node:     %s\nAccount:  %s\n\n", cfg.Node.Alias, cfg.Account.Alias)
	fmt.Fprintf(&b, "5h:       %s\n", windowLine(state.Windows.FiveHour, now, zone))
	fmt.Fprintf(&b, "7d:       %s\n\n", windowLine(state.Windows.SevenDay, now, zone))
	fmt.Fprintf(&b, "Last observation:  %s\n", ago(state.LastObservedAt, now))
	var publishedAt *time.Time
	if state.LastPublished != nil {
		publishedAt = &state.LastPublished.CapturedAt
	}
	fmt.Fprintf(&b, "Last publish:      %s\n\n", ago(publishedAt, now))
	fmt.Fprintf(&b, "Pending events:    %d\nDead letters:      %d\n\n", pending, dead)
	for _, s := range cfg.Sinks {
		fmt.Fprintf(&b, "%-18s %s\n", s.ID+":", flushLine(state.LastFlush, now))
	}
	return b.String()
}

func windowLine(w quota.Window, now time.Time, zone *time.Location) string {
	switch w.Status {
	case quota.WindowObserved:
		percent := strconv.FormatFloat(*w.UsedPercentage, 'f', -1, 64) + "%"
		return fmt.Sprintf("%-10s Reset:  %s", percent, resetLabel(*w.ResetsAt, now, zone))
	case quota.WindowExpired:
		if w.ResetsAt == nil {
			return "expired"
		}
		return fmt.Sprintf("%-10s Reset:  %s (passed)", "expired", resetLabel(*w.ResetsAt, now, zone))
	default:
		return "unknown"
	}
}

func resetLabel(at, now time.Time, zone *time.Location) string {
	local := at.In(zone)
	if local.Year() == now.In(zone).Year() && local.YearDay() == now.In(zone).YearDay() {
		return local.Format("15:04")
	}
	return local.Format("Jan 2 15:04")
}

func ago(at *time.Time, now time.Time) string {
	if at == nil {
		return "never"
	}
	d := now.Sub(*at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func flushLine(record *store.FlushRecord, now time.Time) string {
	if record == nil {
		return "never flushed"
	}
	if record.OK {
		return "last flush ok " + ago(&record.At, now)
	}
	return "last flush failed " + ago(&record.At, now) + ": " + record.Error
}
```

Add `case "status": return runStatus(streams.Stdout, time.Now(), time.Local)` to the `switch` in `cli.go` and import `"time"` there.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/cli/` then `make check`.
Expected: `ok`. If a golden differs by one character, fix the renderer, never the golden, unless the golden itself contradicts the spec's example in `cli-and-install.md`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "$(cat <<'EOF'
feat: add the offline status view

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Flip the spec markers and close the plan

**Files:**
- Modify: `docs/spec/gaugewire/hot-path.md:3`, `docs/spec/gaugewire/spool-and-flush.md` (line 3, "At a glance", quarantine sentence), `docs/spec/gaugewire/cli-and-install.md:3`, `docs/spec/gaugewire/data-contract.md` ("At a glance" sentence), `docs/spec/gaugewire/testing-strategy.md` ("Built so far"), `docs/spec/gaugewire/README.md` (Pages table stays; markers), `docs/spec/README.md`, `docs/nice-to-have.md`
- Delete: `docs/plans/2026-09-18-hot-path-and-flusher.md`

- [ ] **Step 1: Make the pages state the tree**

Read each page against the code before editing:

- `hot-path.md`: `Draft · Built`. Verify every rule (1–8) against `internal/cli/statusline.go`, `observe.go`, `spawn.go`, `internal/renderer`; rule 8's p95 budget is unmeasured, so change it to "Budget: p95 under 50 ms of Gaugewire's own work, excluding the renderer; measured in the acceptance test."
- `spool-and-flush.md`: `Draft · Built`; remove the "flusher process is not [built]" sentence in "At a glance"; the quarantine step reads: "a pending file that cannot be decoded is renamed to `dead-letter/<name>.unreadable` before delivery, so a corrupt file never blocks the others; `doctor` reports such files." Confirm the Mermaid diagram's decode branch matches.
- `cli-and-install.md`: `Draft · Partial` and add after the Commands table: "Built: `statusline`, `flush`, `status`, `version`. Not built: `install`, `uninstall`, `doctor`, `databox bootstrap`."
- `data-contract.md`: change the "At a glance" sentence to "The snapshot, state, spooled-event and config shapes are built; the identity wiring runs through `config.json`."
- `testing-strategy.md`: "Built so far: the `quota`, `source/claude`, `store`, `config`, `renderer`, `sink` and `cli` rows for the commands that exist; integration tests for the parallel hot path and the detached flusher."
- `docs/spec/gaugewire/README.md`: stays `Draft · Partial`.
- `docs/spec/README.md`: gaugewire row scope sentence gains "Hot path and flusher built; install, doctor and the Databox sink are next."
- `docs/nice-to-have.md`: add an entry "Databox sink unavailable in flush is a warning only" is not needed; instead add: **What:** `status` shows the newest dead-letter reason. **Why deferred:** needs a dead-letter reader that tolerates `.unreadable` files. **Trigger:** `doctor` lands. **Reference:** spool-and-flush.md.

- [ ] **Step 2: Delete this plan**

```bash
git rm docs/plans/2026-09-18-hot-path-and-flusher.md
```

- [ ] **Step 3: Verify and commit**

Run the docs link check over `docs/` and `make check`.

```bash
git add docs
git commit -m "$(cat <<'EOF'
docs: mark the hot path and flusher as built

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

Then, with the owner's approval: push the branch and open the pull request with `gh pr create --assignee sulcer --label patch` and a Summary/Test plan body.
