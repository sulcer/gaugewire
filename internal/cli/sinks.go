package cli

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/sink"
	"github.com/sulcer/gaugewire/internal/sink/databox"
)

// buildSinks turns the enabled sink configurations into sinks. A sink that
// cannot be built is logged with the reason and skipped; its events stay
// spooled until the next run.
func buildSinks(cfg config.Config, home string, logger *slog.Logger) []sink.Sink {
	sinks := make([]sink.Sink, 0, len(cfg.Sinks))
	for _, s := range cfg.Sinks {
		if !s.Enabled {
			continue
		}
		built, err := buildDataboxSink(s, home, logger)
		if err != nil {
			logger.Warn("sink not built", "sink", s.ID, "reason", err.Error())
			continue
		}
		sinks = append(sinks, built)
	}
	return sinks
}

// buildDataboxSink assembles one Databox sink. config.Validate already rejects
// every other type, so the type needs no switch until a second one exists.
func buildDataboxSink(s config.Sink, home string, logger *slog.Logger) (sink.Sink, error) {
	// Checked before the key is read so a sink that cannot deliver never holds
	// a key in memory and never builds a client pointed at the default host.
	if s.HistoryDatasetID == "" || s.CurrentDatasetID == "" {
		return nil, errors.New("both dataset ids are required; run gaugewire databox bootstrap")
	}
	key, warn, err := loadAPIKey(s.Credentials, os.Getenv)
	if err != nil {
		return nil, err
	}
	if warn != "" {
		logger.Warn("key file permissions", "sink", s.ID, "reason", warn)
	}
	base := s.BaseURL
	if base == "" {
		base = databox.DefaultBaseURL
	}
	client, err := databox.NewClient(base, key, nil)
	if err != nil {
		return nil, err
	}
	return databox.New(s.ID, client, databox.Datasets{History: s.HistoryDatasetID, Current: s.CurrentDatasetID}, sink.StateIngestions{Home: home}, time.Now, logger)
}
