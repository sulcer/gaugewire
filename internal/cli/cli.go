// Package cli implements the gaugewire subcommands.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
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
var ErrUsage = errors.New("usage: gaugewire <command>\n\ncommands:\n  statusline          Claude Code status-line adapter (reads stdin)\n  flush [--requeue]   deliver pending events once\n  install [--settings path] [--node-alias a] [--account-alias b] [--force]\n                      install the status-line adapter\n  uninstall [--purge] [--settings path]\n                      restore the previous status line\n  status              show quota state and spool counts\n  doctor [--settings path]  run every health check, exit 1 on failure\n  databox bootstrap [--account-id n] [--api-key-file path] [--base-url url] [--sink-id id] [--test-ingest]\n                      create or reuse the Databox data source and datasets\n  version             print version, commit and build date")

// Run executes the command named by args.
func Run(ctx context.Context, args []string, info BuildInfo, streams IO) error {
	if len(args) == 0 {
		return ErrUsage
	}
	switch args[0] {
	case "statusline":
		return runStatusline(ctx, info, streams, spawnFlusher)
	case "flush":
		return runFlush(ctx, args[1:], info, streams)
	case "install":
		return runInstall(ctx, args[1:], info, streams)
	case "uninstall":
		return runUninstall(ctx, args[1:], info, streams)
	case "status":
		return runStatus(streams.Stdout, time.Now(), time.Local)
	case "doctor":
		return runDoctor(ctx, args[1:], info, streams)
	case "databox":
		return runDatabox(ctx, args[1:], info, streams)
	case "version":
		return runVersion(info, streams.Stdout)
	default:
		return fmt.Errorf("unknown command %q: %w", args[0], ErrUsage)
	}
}
