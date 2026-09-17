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
	if incoming != nil {
		if !accepts(stored, *incoming) {
			return stored
		}
		used := incoming.UsedPercentage
		resetsAt := incoming.ResetsAt
		return Window{Status: WindowObserved, UsedPercentage: &used, ResetsAt: &resetsAt}
	}
	if stored.ResetsAt == nil || stored.Status == WindowUnknown {
		return Window{Status: WindowUnknown}
	}
	if stored.ResetsAt.After(now) {
		return stored
	}
	return Window{Status: WindowExpired, ResetsAt: stored.ResetsAt}
}

// accepts is the staleness guard. Every Claude Code session re-sends its own
// last-known values, so an idle session must not overwrite an active one: an
// incoming value replaces an observed window only when its reset is newer, or
// equal with usage at or above the stored value.
func accepts(stored Window, incoming Reading) bool {
	if stored.Status != WindowObserved || stored.ResetsAt == nil || stored.UsedPercentage == nil {
		return true
	}
	if incoming.ResetsAt.After(*stored.ResetsAt) {
		return true
	}
	if incoming.ResetsAt.Equal(*stored.ResetsAt) {
		return incoming.UsedPercentage >= *stored.UsedPercentage
	}
	return false
}
