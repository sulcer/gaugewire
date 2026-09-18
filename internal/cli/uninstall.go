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
// ours, clears the install record, and deletes the home directory when purge
// is set. The restored value is the recorded original compacted onto one line,
// because config.Save re-indents it and its original source bytes are gone;
// every other byte of the settings file is left untouched.
func uninstall(home, settingsPath string, purge bool, stdout io.Writer) error {
	cfg, err := config.Load(home)
	if errors.Is(err, config.ErrMissing) {
		return ErrNotInstalled
	}
	if err != nil {
		return err
	}
	if cfg.Install == nil {
		return ErrNotInstalled
	}
	if settingsPath == "" {
		settingsPath = cfg.Install.SettingsPath
	}
	file, existed, err := settings.Load(settingsPath)
	if err != nil {
		return err
	}
	if !existed {
		fmt.Fprintf(stdout, "warning: %s does not exist; nothing restored\n", settingsPath)
		return purgeHome(home, purge, stdout)
	}
	current, err := settings.Get(file, "statusLine")
	if err != nil {
		return err
	}
	hadNone := len(cfg.Install.OriginalStatusLine) == 0 || string(cfg.Install.OriginalStatusLine) == "null"
	var original []byte
	if !hadNone {
		original, err = compactJSONValue(cfg.Install.OriginalStatusLine)
		if err != nil {
			return err
		}
	}
	done, err := alreadyRestored(current, original, hadNone)
	if err != nil {
		return err
	}
	if done {
		fmt.Fprintf(stdout, "already restored: %s\n", settingsPath)
		return clearRecord(home, cfg, purge, stdout)
	}
	if commandOf(current.Value) != cfg.Install.InstalledCommand {
		fmt.Fprintln(stdout, "warning: statusLine was changed since install; nothing restored")
		return purgeHome(home, purge, stdout)
	}
	var restored []byte
	if hadNone {
		restored, err = settings.Delete(file, "statusLine")
	} else {
		restored, err = settings.Set(file, "statusLine", original)
	}
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
	return clearRecord(home, cfg, purge, stdout)
}

// alreadyRestored reports whether the settings file already holds what install
// replaced. A run that wrote the settings file but died before saving
// config.json leaves exactly this state, and the next run finishes the job.
func alreadyRestored(current settings.Member, original []byte, hadNone bool) (bool, error) {
	if hadNone {
		return !current.Found, nil
	}
	if !current.Found {
		return false, nil
	}
	currentValue, err := compactJSONValue(current.Value)
	if err != nil {
		return false, err
	}
	return bytes.Equal(currentValue, original), nil
}

// clearRecord drops the install record, or deletes the whole home directory
// when purge is set, which makes saving config.json pointless.
func clearRecord(home string, cfg config.Config, purge bool, stdout io.Writer) error {
	if purge {
		return purgeHome(home, true, stdout)
	}
	cfg.Install = nil
	return config.Save(home, cfg)
}

// purgeHome deletes the home directory when purge is set. --purge is explicit
// and unconditional, so it runs even when nothing could be restored.
func purgeHome(home string, purge bool, stdout io.Writer) error {
	if !purge {
		return nil
	}
	if err := os.RemoveAll(home); err != nil {
		return fmt.Errorf("purge home: %w", err)
	}
	fmt.Fprintf(stdout, "purged: %s\n", home)
	return nil
}

func compactJSONValue(raw json.RawMessage) ([]byte, error) {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return nil, fmt.Errorf("compact status line: %w", err)
	}
	return out.Bytes(), nil
}
