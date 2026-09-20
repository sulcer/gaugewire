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
	"time"
)

// waitDelay bounds the wait for the output pipes after the shell exits, so a
// grandchild that inherited them cannot hold the status line open.
const waitDelay = 500 * time.Millisecond

// Run executes command through the platform shell with stdin as its input,
// streaming stdout and stderr to the given writers. An empty command is a no-op.
func Run(ctx context.Context, command string, stdin []byte, stdout, stderr io.Writer) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	name, args := shellCommand(command)
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: the command is the user's own configured status line
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = waitDelay
	isolate(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("renderer: %w", err)
	}
	return nil
}
