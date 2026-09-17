// Package cli implements the gaugewire subcommands.
package cli

import (
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

// ErrUsage is returned when the arguments do not name a valid command.
var ErrUsage = errors.New("usage: gaugewire <command>\n\ncommands:\n  version   print version, commit and build date")

// Run executes the command named by args and writes its output to stdout.
func Run(args []string, info BuildInfo, stdout io.Writer) error {
	if len(args) == 0 {
		return ErrUsage
	}
	switch args[0] {
	case "version":
		return runVersion(info, stdout)
	default:
		return fmt.Errorf("unknown command %q: %w", args[0], ErrUsage)
	}
}
