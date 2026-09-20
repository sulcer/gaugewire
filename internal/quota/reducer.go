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
	// A five-hour window is five hours long, so a payload whose five-hour
	// window has not reset yet was taken within the last five hours. That is
	// what makes it recent enough to move a window's reset, its own included.
	recent := obs.FiveHour != nil && obs.FiveHour.ResetsAt.After(obs.CapturedAt)
	state.Windows.FiveHour = reduceWindow(state.Windows.FiveHour, obs.FiveHour, obs.CapturedAt, recent)
	state.Windows.SevenDay = reduceWindow(state.Windows.SevenDay, obs.SevenDay, obs.CapturedAt, recent)
	captured := obs.CapturedAt
	state.LastObservedAt = &captured
	state.ClaudeCodeVersion = obs.ClaudeCodeVersion
	return state
}

func reduceWindow(stored Window, incoming *Reading, now time.Time, recent bool) Window {
	if incoming != nil && accepts(stored, *incoming, now, recent) {
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
// readings of one window, and after the subscription's window schedule changes
// it also sees readings of a window that no longer applies.
//
// A reading whose reset has passed describes a window that has ended and says
// nothing about the one open now. Windows partition time, so under one
// schedule every payload that still has time on the clock names the same
// reset: two open resets mean two schedules, and only a payload from the last
// five hours can say which one holds. Usage rises within a window, so among
// readings of one window the highest value is the current one and a session
// behind the others cannot pull it back.
func accepts(stored Window, incoming Reading, now time.Time, recent bool) bool {
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
	return recent
}
