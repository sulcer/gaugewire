package cli

import (
	"log/slog"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/sink"
)

// buildSinks turns the enabled sink configurations into sinks. A type this
// build cannot construct is logged and skipped; its events stay spooled.
func buildSinks(cfg config.Config, logger *slog.Logger) []sink.Sink {
	sinks := make([]sink.Sink, 0, len(cfg.Sinks))
	for _, s := range cfg.Sinks {
		if !s.Enabled {
			continue
		}
		logger.Warn("sink type not available in this build", "sink", s.ID, "type", s.Type)
	}
	return sinks
}
