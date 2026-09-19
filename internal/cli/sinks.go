package cli

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/sink"
	"github.com/sulcer/gaugewire/internal/sink/databox"
)

// newDataboxClient builds the client for a sink, defaulting the base URL.
func newDataboxClient(s config.Sink, key string, httpClient *http.Client) (*databox.Client, error) {
	base := s.BaseURL
	if base == "" {
		base = databox.DefaultBaseURL
	}
	return databox.NewClient(base, key, httpClient)
}

// buildSinks turns the enabled sink configurations into sinks. A sink that
// cannot be built is logged with the reason and left out, and the reasons are
// returned joined so the flusher can fail the run; its events stay spooled.
func buildSinks(cfg config.Config, home string, logger *slog.Logger) ([]sink.Sink, error) {
	sinks := make([]sink.Sink, 0, len(cfg.Sinks))
	var errs []error
	for _, s := range cfg.Sinks {
		if !s.Enabled {
			continue
		}
		built, err := buildDataboxSink(s, home, logger)
		if err != nil {
			logger.Warn("sink not built", "sink", s.ID, "reason", err.Error())
			errs = append(errs, fmt.Errorf("sink %s not built: %w", s.ID, err))
			continue
		}
		sinks = append(sinks, built)
	}
	return sinks, errors.Join(errs...)
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
	client, err := newDataboxClient(s, key, nil)
	if err != nil {
		return nil, err
	}
	return databox.New(s.ID, client, databox.Datasets{History: s.HistoryDatasetID, Current: s.CurrentDatasetID}, sink.StateIngestions{Home: home}, time.Now, logger)
}
