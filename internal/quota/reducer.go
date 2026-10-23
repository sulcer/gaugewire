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

// Reduce folds one observation into the state; the caller persists the result.
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
	undated  age = iota // cannot place itself in time, or older than the newest seen
	asRecent            // level with the newest seen
	newer               // past it
)

func reduceWindow(stored Window, incoming *Reading, now time.Time, clock age) Window {
	if incoming != nil && accepts(stored, *incoming, now, clock) {
		used := incoming.UsedPercentage
		resetsAt := incoming.ResetsAt
		return Window{Status: WindowObserved, UsedPercentage: &used, ResetsAt: &resetsAt}
	}
	return expire(stored, now)
}

// payloadAge dates a payload by its own five-hour window: that window is at most
// five hours long and usage only rises within it, so reset and usage together
// order payloads by age. A later reset while the stored window is still running
// is that window re-anchored, not a later payload. A payload with no five-hour
// window dates nothing and counts only while the machine has never seen one,
// because the subscription may have no five-hour limit; even then it is never
// newer. See docs/spec/gaugewire/reducer-and-dedupe.md.
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

// expire keeps a window that is still open and retires one whose reset has
// passed: a value from a window that has ended says nothing about the one open now.
func expire(stored Window, now time.Time) Window {
	if stored.ResetsAt == nil || stored.Status == WindowUnknown {
		return Window{Status: WindowUnknown}
	}
	if stored.ResetsAt.After(now) {
		return stored
	}
	return Window{Status: WindowExpired, ResetsAt: stored.ResetsAt}
}

// accepts is the staleness guard: every open session reports the rate limits it
// last received, so one machine sees many readings of one window and, after the
// schedule changes, readings of a window that no longer applies. A reading of
// the window already stored refines it whatever the payload's age, since usage
// only rises within a window. Taking a window the machine holds open needs a
// payload newer than any seen so far, so two sessions cannot trade it on every
// tick; naming a window it does not hold asks only for a dated payload.
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
