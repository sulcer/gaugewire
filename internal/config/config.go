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
const DefaultAPIKeyEnv = "DATABOX_API_KEY" //nolint:gosec // names an env var, not a credential value

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
