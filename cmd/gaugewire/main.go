// Command gaugewire observes Claude Code subscription quota and publishes it to sinks.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/sulcer/gaugewire/internal/cli"
)

// Set by the linker at release time; see .goreleaser.yaml.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	info := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	err := cli.Run(os.Args[1:], info, os.Stdout)
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, cli.ErrUsage) {
		os.Exit(2)
	}
	os.Exit(1)
}
