package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestLoadReturnsTheDecodedConfigWithAValidationError(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeConfig(t, home, strings.Replace(specExample, `"id": "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f"`, `"id": "not-a-uuid"`, 1))
	got, err := Load(home)
	gotSummary := struct {
		invalid bool
		command string
	}{errors.Is(err, ErrInvalid), got.Renderer.Command}
	want := struct {
		invalid bool
		command string
	}{true, "bash ~/.claude/statusline-command.sh"}
	if gotSummary != want {
		t.Fatalf("got %+v, want %+v", gotSummary, want)
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
		{"default without sinks is valid once identities are set", func(c *Config) {
			*c = Default()
			c.Node = Identity{ID: "6f1e2d3c-4b5a-4c6d-8e7f-9a0b1c2d3e4f", Alias: "n"}
			c.Account = Identity{ID: "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", Alias: "a"}
		}, true},
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
