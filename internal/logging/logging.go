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

// Open returns a JSON logger appending to logs/gaugewire.log, rotating it to .1
// at one MiB, plus a close function. A second Open may rotate the file from
// under the first writer, whose later lines then land in .1: these processes are
// short-lived.
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
