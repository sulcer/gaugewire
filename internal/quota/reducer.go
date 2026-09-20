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
	clock := payloadAge(state.Windows.FiveHour, obs)
	state.Windows.FiveHour = reduceWindow(state.Windows.FiveHour, obs.FiveHour, obs.CapturedAt, clock)
	state.Windows.SevenDay = reduceWindow(state.Windows.SevenDay, obs.SevenDay, obs.CapturedAt, clock)
	captured := obs.CapturedAt
	state.LastObservedAt = &captured
	state.ClaudeCodeVersion = obs.ClaudeCodeVersion
	return state
}

// age says how a payload compares with the most recent one this machine has
// seen, which is what decides whether it may say that a window has changed.
type age int

const (
	// undated is a payload that cannot place itself in time, or one older than
	// the machine's newest.
	undated age = iota
	// asRecent is a payload level with the machine's newest.
	asRecent
	// newer is a payload past it.
	newer
)

func reduceWindow(stored Window, incoming *Reading, now time.Time, clock age) Window {
	if incoming != nil && accepts(stored, *incoming, now, clock) {
		used := incoming.UsedPercentage
		resetsAt := incoming.ResetsAt
		return Window{Status: WindowObserved, UsedPercentage: &used, ResetsAt: &resetsAt}
	}
	return expire(stored, now)
}

// payloadAge dates a payload by its own five-hour window. That window is at
// most five hours long, so a payload whose five-hour window has not reset yet
// was taken within the last five hours, and the window doubles as a clock:
// within it usage only rises, and a later reset is a later window, so the pair
// orders payloads by age. The stored five-hour window carries the newest pair
// the machine has seen.
//
// A later five-hour reset counts as a later window only once the one the machine
// holds has ended; while that one is still running, a later reset is a window
// re-anchored under the payload, not a payload taken later.
//
// A payload with no five-hour window of its own proves nothing, and counts only
// while this machine has never seen one, because the subscription may have no
// five-hour limit at all. Even then it is never treated as newer, so it cannot
// take a window the machine holds open.
func payloadAge(fiveHour Window, obs Observation) age {
	if obs.FiveHour == nil || !obs.FiveHour.ResetsAt.After(obs.CapturedAt) {
		if fiveHour.Status == WindowUnknown {
			return asRecent
		}
		return undated
	}
	if fiveHour.Status != WindowObserved || fiveHour.ResetsAt == nil || fiveHour.UsedPercentage == nil {
		return newer
	}
	switch {
	case obs.FiveHour.ResetsAt.After(*fiveHour.ResetsAt):
		if fiveHour.ResetsAt.After(obs.CapturedAt) {
			// A five-hour window the machine holds is still running, so a
			// payload announcing a later one was not taken later: its window
			// was re-anchored, exactly as the seven-day schedule was.
			return undated
		}
		return newer
	case !obs.FiveHour.ResetsAt.Equal(*fiveHour.ResetsAt):
		return undated
	case obs.FiveHour.UsedPercentage > *fiveHour.UsedPercentage:
		return newer
	case obs.FiveHour.UsedPercentage == *fiveHour.UsedPercentage:
		return asRecent
	default:
		return undated
	}
}

// expire keeps a window that is still open and retires one whose reset has passed,
// whether the reading that arrived was absent or refused: a value from a window
// that has ended says nothing about the window open now.
func expire(stored Window, now time.Time) Window {
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
// nothing about the one open now. A reading of the window already stored
// refines it whatever the payload's age, because usage only rises within a
// window and a session behind the others reports less. Taking a window the
// machine holds open and calling it something else needs a payload newer than
// any seen so far, so that two sessions cannot take it from each other on
// every tick; naming a window the machine does not hold, because it has ended
// or was never seen, asks only for a payload that can place itself in time.
func accepts(stored Window, incoming Reading, now time.Time, clock age) bool {
	if !incoming.ResetsAt.After(now) {
		return false
	}
	if stored.Status == WindowObserved && stored.ResetsAt != nil && stored.UsedPercentage != nil {
		if incoming.ResetsAt.Equal(*stored.ResetsAt) {
			return incoming.UsedPercentage >= *stored.UsedPercentage
		}
		if stored.ResetsAt.After(now) {
			return clock == newer
		}
	}
	return clock >= asRecent
}
