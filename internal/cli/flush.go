package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/sink"
	"github.com/sulcer/gaugewire/internal/store"
)

// runFlush performs one flusher run and prints a one-line summary.
func runFlush(ctx context.Context, args []string, _ BuildInfo, streams IO) error {
	flags := flag.NewFlagSet("flush", flag.ContinueOnError)
	flags.SetOutput(streams.Stderr)
	requeue := flags.Bool("requeue", false, "move dead-letter events back to pending first")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	if err = store.EnsureLayout(home); err != nil {
		return err
	}
	logger, closeLog := openLogger(home)
	defer closeLog()
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	flusher := sink.Flusher{Home: home, Sinks: buildSinks(cfg, home, logger), Now: time.Now, Random: rand.Float64, Logger: logger, Requeue: *requeue} //nolint:gosec // G404: jitter, not security
	res, runErr := flusher.Run(ctx)
	if res.Skipped {
		fmt.Fprintln(streams.Stdout, "skipped: another flusher is running")
	}
	if *requeue {
		fmt.Fprintf(streams.Stdout, "requeued %d events\n", res.Requeued)
	}
	fmt.Fprintf(streams.Stdout, "delivered %d, retried %d, dead-lettered %d, quarantined %d\n", res.Delivered, res.Retried, res.DeadLettered, res.Quarantined)
	return runErr
}
