package quota

import (
	"math"
	"time"
)

// EventType classifies why a snapshot is published.
type EventType string

const (
	// EventStateTransition is the first observation or a window status change.
	EventStateTransition EventType = "state_transition"
	// EventChange is a reset change or a usage move past the threshold.
	EventChange EventType = "change"
	// EventHeartbeat is a periodic publish while nothing changed.
	EventHeartbeat EventType = "heartbeat"
)

// Publishing holds the dedupe thresholds.
type Publishing struct {
	MinDeltaPercentage float64
	HeartbeatInterval  time.Duration
}

// DefaultPublishing returns the spec defaults: 1 percentage point, 30 minutes.
func DefaultPublishing() Publishing {
	return Publishing{MinDeltaPercentage: 1.0, HeartbeatInterval: 30 * time.Minute}
}

// deltaTolerance is floating-point noise below any percentage the source can report.
const deltaTolerance = 1e-9

// Decision says whether the current state should be published and why.
type Decision struct {
	Publish   bool
	EventType EventType
}

// Decide compares the reduced state with the last published snapshot. The first
// matching rule wins; usage is compared against the last published value, not
// the previous input, so small drifts accumulate.
func Decide(state State, now time.Time, p Publishing) Decision {
	anyObserved := state.Windows.FiveHour.Status == WindowObserved || state.Windows.SevenDay.Status == WindowObserved
	if state.LastPublished == nil {
		if anyObserved {
			return Decision{Publish: true, EventType: EventStateTransition}
		}
		return Decision{}
	}
	last := state.LastPublished.Windows
	current := state.Windows
	if current.FiveHour.Status != last.FiveHour.Status || current.SevenDay.Status != last.SevenDay.Status {
		return Decision{Publish: true, EventType: EventStateTransition}
	}
	if !sameTime(current.FiveHour.ResetsAt, last.FiveHour.ResetsAt) || !sameTime(current.SevenDay.ResetsAt, last.SevenDay.ResetsAt) {
		return Decision{Publish: true, EventType: EventChange}
	}
	if delta(current.FiveHour.UsedPercentage, last.FiveHour.UsedPercentage) >= p.MinDeltaPercentage-deltaTolerance ||
		delta(current.SevenDay.UsedPercentage, last.SevenDay.UsedPercentage) >= p.MinDeltaPercentage-deltaTolerance {
		return Decision{Publish: true, EventType: EventChange}
	}
	if anyObserved && now.Sub(state.LastPublished.CapturedAt) >= p.HeartbeatInterval {
		return Decision{Publish: true, EventType: EventHeartbeat}
	}
	return Decision{}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func delta(a, b *float64) float64 {
	if a == nil || b == nil {
		return 0
	}
	return math.Abs(*a - *b)
}
