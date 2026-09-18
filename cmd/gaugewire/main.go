// Command gaugewire observes Claude Code subscription quota and publishes it to sinks.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/sulcer/gaugewire/internal/cli"
)

// Set by the linker at release time; see .goreleaser.yaml.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	info := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	err := cli.Run(ctx, os.Args[1:], info, cli.IO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, cli.ErrUsage) {
		return 2
	}
	return 1
}
