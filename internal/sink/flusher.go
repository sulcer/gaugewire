package sink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/sulcer/gaugewire/internal/store"
)

// FlushLockFile guarantees one flusher per machine.
const FlushLockFile = "flush.lock"

// RequestTimeout bounds one HTTP request a sink makes.
const RequestTimeout = 15 * time.Second

// BatchTimeout bounds one PublishBatch call, which may make several requests.
const BatchTimeout = 3 * RequestTimeout

// RunTimeout bounds one flusher run.
const RunTimeout = 2 * time.Minute

// Result summarises one flusher run.
type Result struct {
	Delivered    int
	Retried      int
	DeadLettered int
	Quarantined  int
	Requeued     int
	Skipped      bool
}

// Flusher delivers due pending events to every sink, in chronological order,
// chunked, with backoff on transient failures and dead letters on permanent ones.
type Flusher struct {
	Home    string
	Sinks   []Sink
	Now     func() time.Time
	Random  func() float64
	Logger  *slog.Logger
	Requeue bool
}

// Run performs one pass and exits. It never sleeps until the next attempt; a
// later status-line invocation relaunches the flusher when work is due. Requeue
// moves dead letters back first, under the same lock, so a concurrent
// dead-letter can never lose an event.
func (f Flusher) Run(ctx context.Context) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, RunTimeout)
	defer cancel()
	unlock, held, err := store.TryLock(filepath.Join(f.Home, FlushLockFile))
	if err != nil {
		return Result{}, err
	}
	if !held {
		f.Logger.Info("flush skipped", "reason", "another flusher is running")
		return Result{Skipped: true}, nil
	}
	defer func() { _ = unlock() }()

	var res Result
	now := f.Now().UTC()
	if f.Requeue {
		requeued, requeueErr := store.Requeue(f.Home, now)
		res.Requeued = requeued
		if requeueErr != nil {
			return res, requeueErr
		}
	}
	moved, err := store.Quarantine(f.Home)
	res.Quarantined = len(moved)
	for _, name := range moved {
		f.Logger.Warn("pending event quarantined", "file", name)
	}
	if err != nil {
		return res, err
	}
	events, err := store.ListPending(f.Home)
	if err != nil {
		return res, err
	}
	var runErr error
	for _, s := range f.Sinks {
		if err := f.deliver(ctx, s, events, now, &res); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	f.recordFlush(ctx, now, runErr)
	f.Logger.Info("flush finished", "delivered", res.Delivered, "retried", res.Retried, "deadLettered", res.DeadLettered, "quarantined", res.Quarantined)
	return res, runErr
}

func (f Flusher) deliver(ctx context.Context, s Sink, events []store.PendingEvent, now time.Time, res *Result) error {
	due := make([]int, 0, len(events))
	for i, pe := range events {
		d, targeted := pe.Event.Delivery[s.ID()]
		if targeted && !d.NextAttemptAt.After(now) {
			due = append(due, i)
		}
	}
	for start := 0; start < len(due); start += MaxBatch {
		chunk := due[start:min(start+MaxBatch, len(due))]
		deliveries := make([]Delivery, 0, len(chunk))
		for _, i := range chunk {
			deliveries = append(deliveries, Delivery{EventType: events[i].Event.EventType, Snapshot: events[i].Event.Snapshot})
		}
		callCtx, cancel := context.WithTimeout(ctx, BatchTimeout)
		err := s.PublishBatch(callCtx, deliveries)
		cancel()
		if err == nil {
			if ackErr := f.acknowledge(s.ID(), events, chunk); ackErr != nil {
				return ackErr
			}
			res.Delivered += len(chunk)
			continue
		}
		class, code := Classify(err)
		if class == Permanent {
			for _, i := range chunk {
				if dlErr := store.DeadLetter(f.Home, events[i], s.ID()+": "+err.Error(), now); dlErr != nil {
					return dlErr
				}
				// A dead-lettered event is out of circulation until requeued: clear
				// its delivery map so no later sink in this run finds it targeted
				// and recreates the pending file next to the dead-letter copy.
				events[i].Event.Delivery = map[string]store.DeliveryState{}
				res.DeadLettered++
				f.Logger.Error("event dead-lettered", "eventId", events[i].Event.Snapshot.EventID, "sink", s.ID(), "code", code)
			}
			continue
		}
		for _, i := range chunk {
			d := events[i].Event.Delivery[s.ID()]
			d.Attempts++
			d.NextAttemptAt = NextAttempt(d.Attempts, now, f.Random)
			d.LastError = err.Error()
			d.LastErrorCode = code
			events[i].Event.Delivery[s.ID()] = d
			if updErr := store.UpdatePending(events[i]); updErr != nil {
				return updErr
			}
			res.Retried++
		}
		f.Logger.Warn("delivery deferred", "sink", s.ID(), "events", len(chunk), "attempt", events[chunk[0]].Event.Delivery[s.ID()].Attempts, "code", code)
		return fmt.Errorf("sink %s: %w", s.ID(), err)
	}
	return nil
}

func (f Flusher) acknowledge(sinkID string, events []store.PendingEvent, chunk []int) error {
	for _, i := range chunk {
		delete(events[i].Event.Delivery, sinkID)
		if len(events[i].Event.Delivery) == 0 {
			if err := store.DeletePending(events[i]); err != nil {
				return err
			}
			continue
		}
		if err := store.UpdatePending(events[i]); err != nil {
			return err
		}
	}
	return nil
}

func (f Flusher) recordFlush(ctx context.Context, now time.Time, runErr error) {
	unlock, err := store.Lock(ctx, filepath.Join(f.Home, store.StateLockFile), time.Second)
	if err != nil {
		f.Logger.Warn("flush record skipped", "reason", err.Error())
		return
	}
	defer func() { _ = unlock() }()
	state, err := store.LoadState(f.Home)
	if err != nil {
		if !errors.Is(err, store.ErrStateCorrupt) {
			f.Logger.Warn("flush record skipped", "reason", err.Error())
			return
		}
		f.Logger.Warn("state reset", "reason", err.Error())
	}
	record := &store.FlushRecord{At: now, OK: runErr == nil}
	if runErr != nil {
		record.Error = runErr.Error()
	}
	state.LastFlush = record
	if err := store.SaveState(f.Home, state); err != nil {
		f.Logger.Warn("flush record not saved", "reason", err.Error())
	}
}
