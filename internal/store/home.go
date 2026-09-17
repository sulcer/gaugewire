// Package store owns everything Gaugewire keeps on disk: the home directory,
// atomic file writes, advisory locks, the machine state and the event spool.
package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// HomeEnv overrides the home directory when set.
const HomeEnv = "GAUGEWIRE_HOME"

// Directory names inside the home directory.
const (
	PendingDir    = "pending"
	DeadLetterDir = "dead-letter"
	LogsDir       = "logs"
)

const dirName = "gaugewire"

// Home returns the directory holding config, state, locks, spool and logs:
// GAUGEWIRE_HOME when set, else the platform user config dir plus "gaugewire".
func Home() (string, error) {
	if override := os.Getenv(HomeEnv); override != "" {
		return override, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(base, dirName), nil
}

// EnsureLayout creates the home directory and its subdirectories with owner-only
// permissions. It is safe to call on every run.
func EnsureLayout(home string) error {
	for _, dir := range []string{home, filepath.Join(home, PendingDir), filepath.Join(home, DeadLetterDir), filepath.Join(home, LogsDir)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}
