package cli

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"runtime"
	"time"
	"uuid"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/source/claude"
	"github.com/sulcer/gaugewire/internal/store"
)

const lockWait = time.Second

type observeResult struct {
	Published bool
	EventID   string
	Spawn     bool
}

// observe is the observing part of the hot path: parse, reduce, decide, spool,
// persist. Every failure is logged and swallowed; the renderer must never wait.
func observe(ctx context.Context, home string, cfg config.Config, payload []byte, now time.Time, info BuildInfo, logger *slog.Logger) observeResult {
	obs, issues, err := claude.Parse(bytes.NewReader(payload), now)
	if err != nil {
		logger.Info("observation skipped", "reason", err.Error())
		return observeResult{}
	}
	for _, issue := range issues {
		logger.Warn("window ignored", "path", issue)
	}
	if layoutErr := store.EnsureLayout(home); layoutErr != nil {
		logger.Error("home directory unavailable", "error", layoutErr.Error())
		return observeResult{}
	}
	unlock, err := store.Lock(ctx, filepath.Join(home, store.StateLockFile), lockWait)
	if err != nil {
		logger.Warn("observation dropped", "reason", err.Error())
		return observeResult{}
	}
	res, _ := reduceAndSpool(home, cfg, obs, now, info, logger)
	if unlockErr := unlock(); unlockErr != nil {
		logger.Warn("unlock failed", "error", unlockErr.Error())
	}
	if !res.Spawn {
		res.Spawn = hasDueWork(home, now)
	}
	return res
}

func reduceAndSpool(home string, cfg config.Config, obs quota.Observation, now time.Time, info BuildInfo, logger *slog.Logger) (observeResult, []string) {
	state, err := store.LoadState(home)
	if err != nil {
		logger.Warn("state reset", "reason", err.Error())
	}
	state.State = quota.Reduce(state.State, obs)
	decision := quota.Decide(state.State, now, cfg.QuotaPublishing())
	targets := cfg.EnabledSinkIDs()
	var res observeResult
	if decision.Publish {
		eventID := uuid.NewV4().String()
		snapshot := quota.NewSnapshot(cfg.QuotaIdentity(runtime.GOOS, info.Version), state.State, eventID, obs.CapturedAt)
		if len(targets) > 0 {
			ev := store.Event{EventType: decision.EventType, Snapshot: snapshot, Delivery: make(map[string]store.DeliveryState, len(targets))}
			for _, id := range targets {
				ev.Delivery[id] = store.DeliveryState{NextAttemptAt: now.UTC()}
			}
			if _, err := store.WritePending(home, ev); err != nil {
				logger.Error("event not spooled", "eventId", eventID, "error", err.Error())
				return res, targets
			}
			res.Spawn = true
		}
		state.State = quota.MarkPublished(state.State, eventID, snapshot.CapturedAt)
		res.Published = true
		res.EventID = eventID
		logger.Info("event published", "eventId", eventID, "eventType", string(decision.EventType), "targets", len(targets))
	}
	if err := store.SaveState(home, state); err != nil {
		logger.Error("state not saved", "error", err.Error())
	}
	return res, targets
}

// hasDueWork reports whether any pending event is due. A listing error means a
// file needs quarantining, which is the flusher's job, so it counts as due.
func hasDueWork(home string, now time.Time) bool {
	events, err := store.ListPending(home)
	if err != nil {
		return true
	}
	for _, pe := range events {
		for _, d := range pe.Event.Delivery {
			if !d.NextAttemptAt.After(now) {
				return true
			}
		}
	}
	return false
}
