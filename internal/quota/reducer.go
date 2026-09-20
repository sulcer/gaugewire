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
	dated := dated(state.Windows.FiveHour, obs)
	state.Windows.FiveHour = reduceWindow(state.Windows.FiveHour, obs.FiveHour, obs.CapturedAt, dated)
	state.Windows.SevenDay = reduceWindow(state.Windows.SevenDay, obs.SevenDay, obs.CapturedAt, dated)
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

// dated reports whether a payload may say which window is open. A five-hour
// window is at most five hours long, so a payload whose five-hour window has
// not reset yet was taken within the last five hours, and that window doubles
// as the payload's clock: within one five-hour window its usage only rises,
// and a later reset is a later window, so the pair orders payloads by age. A
// payload counts when it is at least as recent as the one that left the stored
// five-hour window, which is the most recent this machine has seen.
//
// A payload with no five-hour window of its own proves nothing, and is trusted
// only while this machine has never seen one: the subscription may have no
// five-hour limit at all.
func dated(fiveHour Window, obs Observation) bool {
	if obs.FiveHour == nil || !obs.FiveHour.ResetsAt.After(obs.CapturedAt) {
		return fiveHour.Status == WindowUnknown
	}
	if fiveHour.Status != WindowObserved || fiveHour.ResetsAt == nil || fiveHour.UsedPercentage == nil {
		return true
	}
	if obs.FiveHour.ResetsAt.After(*fiveHour.ResetsAt) {
		return true
	}
	return obs.FiveHour.ResetsAt.Equal(*fiveHour.ResetsAt) && obs.FiveHour.UsedPercentage >= *fiveHour.UsedPercentage
}

// accepts is the staleness guard. Every open Claude Code session runs the
// status line with the rate limits it last received, so one machine sees many
// readings of one window, and after the subscription's window schedule changes
// it also sees readings of a window that no longer applies.
//
// A reading whose reset has passed describes a window that has ended and says
// nothing about the one open now. A reading of the window already stored
// refines it whatever the payload's age, because usage only rises within a
// window and a session behind the others reports less. Saying that a different
// window is open replaces what the machine reports, so it takes a dated
// payload: otherwise a session that stopped days ago could name the window,
// and two sessions could take it from each other on every tick.
func accepts(stored Window, incoming Reading, now time.Time, dated bool) bool {
	if !incoming.ResetsAt.After(now) {
		return false
	}
	if stored.Status == WindowObserved && stored.ResetsAt != nil && stored.UsedPercentage != nil &&
		incoming.ResetsAt.Equal(*stored.ResetsAt) {
		return incoming.UsedPercentage >= *stored.UsedPercentage
	}
	return dated
}
