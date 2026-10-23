package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/store"
)

// spawnFlusher starts `gaugewire flush` detached from this process: its own
// session, stdin and stdout on the null device, stderr appended to the log file,
// so Claude Code's stdout pipe closes as soon as the hot path exits. Its summary
// line is plain text, which would break the JSON log.
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
	// The flusher outlives this call, so it is not tied to a caller context.
	cmd := exec.CommandContext(context.Background(), exe, "flush")
	cmd.Env = append(os.Environ(), store.HomeEnv+"="+home)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = out
	cmd.SysProcAttr = detachAttrs()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start flusher: %w", err)
	}
	return cmd.Process.Release()
}
