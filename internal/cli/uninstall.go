package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/settings"
	"github.com/sulcer/gaugewire/internal/store"
)

// ErrNotInstalled means config.json records no install to undo.
var ErrNotInstalled = errors.New("gaugewire is not installed on this machine")

// runUninstall parses the flags and undoes the install recorded in config.json.
func runUninstall(_ context.Context, args []string, _ BuildInfo, streams IO) error {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(streams.Stderr)
	purge := flags.Bool("purge", false, "also delete the home directory")
	settingsPath := flags.String("settings", "", "settings file to restore (default: the one recorded at install)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	path := *settingsPath
	if path != "" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve settings path: %w", err)
		}
		path = absolute
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	return uninstall(home, path, *purge, streams.Stdout)
}

// uninstall restores the status line recorded at install when it is still
// ours, clears the install record, and optionally deletes the home directory.
// The restored value is the recorded original compacted onto one line, because
// config.Save re-indents it and its original source bytes are gone; every other
// byte of the settings file is left untouched.
func uninstall(home, settingsPath string, purge bool, stdout io.Writer) error {
	cfg, err := config.Load(home)
	if errors.Is(err, config.ErrMissing) {
		return ErrNotInstalled
	}
	if err != nil && !errors.Is(err, config.ErrInvalid) {
		return err
	}
	if cfg.Install == nil {
		return ErrNotInstalled
	}
	if settingsPath == "" {
		settingsPath = cfg.Install.SettingsPath
	}
	file, _, err := settings.Load(settingsPath)
	if err != nil {
		return err
	}
	current, err := settings.Get(file, "statusLine")
	if err != nil {
		return err
	}
	if commandOf(current.Value) != cfg.Install.InstalledCommand {
		fmt.Fprintln(stdout, "warning: statusLine was changed since install; nothing restored")
		return nil
	}
	restored, hadNone, err := withoutGaugewire(file, cfg.Install.OriginalStatusLine)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(settingsPath); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := store.WriteFileAtomic(settingsPath, restored, mode); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if hadNone {
		fmt.Fprintf(stdout, "removed statusLine from %s\n", settingsPath)
	} else {
		fmt.Fprintf(stdout, "restored: %s\n", settingsPath)
	}
	cfg.Install = nil
	if purge {
		if err := os.RemoveAll(home); err != nil {
			return fmt.Errorf("purge home: %w", err)
		}
		fmt.Fprintf(stdout, "purged: %s\n", home)
		return nil
	}
	return config.Save(home, cfg)
}

// withoutGaugewire puts the recorded original back into the settings document,
// or deletes the statusLine member when install found none. hadNone reports
// which of the two happened.
func withoutGaugewire(file []byte, original json.RawMessage) (restored []byte, hadNone bool, err error) {
	if len(original) == 0 || string(original) == "null" {
		restored, err = settings.Delete(file, "statusLine")
		return restored, true, err
	}
	var compact bytes.Buffer
	if err = json.Compact(&compact, original); err != nil {
		return nil, false, fmt.Errorf("compact original status line: %w", err)
	}
	restored, err = settings.Set(file, "statusLine", compact.Bytes())
	return restored, false, err
}
