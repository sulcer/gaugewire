package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/logging"
	"github.com/sulcer/gaugewire/internal/renderer"
	"github.com/sulcer/gaugewire/internal/store"
)

// runStatusline is the hot path. It reads stdin once, starts the renderer with
// the exact bytes, observes in parallel, spawns the flusher when work is due,
// then forwards the renderer's result. It always returns nil: Claude Code must
// never see a failed status line because of observability.
func runStatusline(ctx context.Context, info BuildInfo, streams IO, spawn func(home string) error) error {
	payload, err := io.ReadAll(streams.Stdin)
	if err != nil {
		return nil
	}
	home, err := store.Home()
	if err != nil {
		return nil
	}
	cfg, err := config.Load(home)
	if errors.Is(err, config.ErrMissing) {
		// Before install there is no config; the home directory stays untouched.
		return nil
	}
	logger, closeLog := openLogger(home)
	defer closeLog()
	if err != nil {
		logger.Error("config unusable", "error", err.Error())
		if rErr := renderer.Run(ctx, cfg.Renderer.Command, payload, streams.Stdout, streams.Stderr); rErr != nil {
			logger.Warn("renderer failed", "error", rErr.Error())
		}
		return nil
	}
	rendered := make(chan error, 1)
	go func() {
		rendered <- renderer.Run(ctx, cfg.Renderer.Command, payload, streams.Stdout, streams.Stderr)
	}()
	res := observe(ctx, home, cfg, payload, time.Now(), info, logger)
	if res.Spawn {
		if err := spawn(home); err != nil {
			logger.Error("flusher not started", "error", err.Error())
		}
	}
	if err := <-rendered; err != nil {
		logger.Warn("renderer failed", "error", err.Error())
	}
	return nil
}

func openLogger(home string) (*slog.Logger, func()) {
	logger, closeLog, err := logging.Open(home)
	if err != nil {
		return logging.Discard(), func() {}
	}
	return logger, func() { _ = closeLog() }
}
