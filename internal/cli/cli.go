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
	_ = ctx // wired to statusline and flush in the next tasks
	if len(args) == 0 {
		return ErrUsage
	}
	switch args[0] {
	case "version":
		return runVersion(info, streams.Stdout)
	default:
		return fmt.Errorf("unknown command %q: %w", args[0], ErrUsage)
	}
}
