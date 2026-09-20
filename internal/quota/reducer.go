package quota

import "time"

// Observation is what a source produced from one status-line invocation.
// A nil window means the source did not report it or reported it invalidly.
type Observation struct {
	CapturedAt        time.Time
	ClaudeCodeVersion string
	FiveHour          *Reading
	SevenDay          *Reading
}

// Reduce folds one observation into the state. It is pure: the caller persists
// the result. Each window is reduced independently.
func Reduce(state State, obs Observation) State {
	state.SchemaVersion = SchemaVersion
	state.Windows.FiveHour = reduceWindow(state.Windows.FiveHour, obs.FiveHour, obs.CapturedAt)
	state.Windows.SevenDay = reduceWindow(state.Windows.SevenDay, obs.SevenDay, obs.CapturedAt)
	captured := obs.CapturedAt
	state.LastObservedAt = &captured
	state.ClaudeCodeVersion = obs.ClaudeCodeVersion
	return state
}

func reduceWindow(stored Window, incoming *Reading, now time.Time) Window {
	if incoming != nil && accepts(stored, *incoming, now) {
		used := incoming.UsedPercentage
		resetsAt := incoming.ResetsAt
		return Window{Status: WindowObserved, UsedPercentage: &used, ResetsAt: &resetsAt}
	}
	return age(stored, now)
}

// age keeps a window that is still open and retires one whose reset has passed,
// whether the reading that arrived was absent or refused: a value from a window
// that has ended says nothing about the window open now.
func age(stored Window, now time.Time) Window {
	if stored.ResetsAt == nil || stored.Status == WindowUnknown {
		return Window{Status: WindowUnknown}
	}
	if stored.ResetsAt.After(now) {
		return stored
	}
	return Window{Status: WindowExpired, ResetsAt: stored.ResetsAt}
}

// accepts is the staleness guard. Every open Claude Code session runs the
// status line with the rate limits it last received, so one machine sees many
// readings of one window and, after the subscription's schedule changes,
// readings of a window that no longer applies. A reading counts when it
// describes the window open now: its reset is still ahead, and among open
// windows the one resetting soonest is the current one, because a later reset
// can only come from a session whose values predate the change. Usage rises
// within a window, so among readings of one window the highest value wins.
func accepts(stored Window, incoming Reading, now time.Time) bool {
	if !incoming.ResetsAt.After(now) {
		return false
	}
	if stored.Status != WindowObserved || stored.ResetsAt == nil || stored.UsedPercentage == nil {
		return true
	}
	if !stored.ResetsAt.After(now) {
		return true
	}
	if incoming.ResetsAt.Equal(*stored.ResetsAt) {
		return incoming.UsedPercentage >= *stored.UsedPercentage
	}
	return incoming.ResetsAt.Before(*stored.ResetsAt)
}
