package quota

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("bad test time %q: %v", value, err)
	}
	return parsed
}

func observed(used float64, resetsAt time.Time) Window {
	return Window{Status: WindowObserved, UsedPercentage: &used, ResetsAt: &resetsAt}
}

func TestReduceWindowStateMachine(t *testing.T) {
	t.Parallel()
	now := at(t, "2026-09-17T15:00:00Z")
	future := at(t, "2026-09-17T18:00:00Z")
	past := at(t, "2026-09-17T14:00:00Z")
	later := at(t, "2026-09-17T23:00:00Z")

	cases := []struct {
		name     string
		stored   Window
		incoming *Reading
		dated    bool
		want     Window
	}{
		{"absent and never observed stays unknown", Window{}, nil, true, Window{Status: WindowUnknown}},
		{"absent and explicitly unknown stays unknown", Window{Status: WindowUnknown}, nil, true, Window{Status: WindowUnknown}},
		{"first open reading from a dated payload is observed", Window{Status: WindowUnknown}, &Reading{42, future}, true, observed(42, future)},
		{"first open reading from an undated payload is ignored", Window{Status: WindowUnknown}, &Reading{42, future}, false, Window{Status: WindowUnknown}},
		{"zero percent is a real value", Window{Status: WindowUnknown}, &Reading{0, future}, true, observed(0, future)},
		{"hundred percent is preserved", Window{Status: WindowUnknown}, &Reading{100, future}, true, observed(100, future)},
		{"decimal percentage is not rounded", Window{Status: WindowUnknown}, &Reading{41.2, future}, true, observed(41.2, future)},
		{"absent with an unexpired reset keeps the stored window", observed(42, future), nil, true, observed(42, future)},
		{"absent with a passed reset expires and keeps the reset for diagnostics", observed(42, past), nil, true, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired stays expired while absent", Window{Status: WindowExpired, ResetsAt: &past}, nil, true, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired then an open reading from a dated payload is observed again", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{7, later}, true, observed(7, later)},
		{"expired and an open reading from an undated payload stays expired", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{7, later}, false, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired and a reading of the window that ended stays expired", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{90, past}, true, Window{Status: WindowExpired, ResetsAt: &past}},
		{"same reset and lower usage is a session behind the others and is ignored", observed(70, future), &Reading{40, future}, true, observed(70, future)},
		{"same reset and higher usage is accepted", observed(40, future), &Reading{70, future}, true, observed(70, future)},
		{"same reset and equal usage is accepted", observed(40, future), &Reading{40, future}, true, observed(40, future)},
		{"an undated payload still refines the window already stored", observed(40, future), &Reading{70, future}, false, observed(70, future)},
		{"a reading whose reset has passed is ignored", observed(70, future), &Reading{90, past}, true, observed(70, future)},
		{"a reading whose reset is exactly now is ignored", observed(70, future), &Reading{90, now}, true, observed(70, future)},
		{"a reading whose reset has passed cannot start a window", Window{Status: WindowUnknown}, &Reading{90, past}, true, Window{Status: WindowUnknown}},
		{"the stored window retires when only passed readings arrive", observed(70, past), &Reading{70, past}, true, Window{Status: WindowExpired, ResetsAt: &past}},
		{"a different open window from a dated payload is adopted", observed(7, later), &Reading{78, future}, true, observed(78, future)},
		{"a different open window from an undated payload is ignored", observed(78, future), &Reading{7, later}, false, observed(78, future)},
		{"a sooner open window from an undated payload is ignored too", observed(2, later), &Reading{7, future}, false, observed(2, later)},
		{"the window after the stored one reset needs a dated payload", observed(70, past), &Reading{3, later}, true, observed(3, later)},
		{"an undated payload cannot name the window after a reset", observed(70, past), &Reading{3, later}, false, Window{Status: WindowExpired, ResetsAt: &past}},
		{"a stored window whose reset is exactly now is replaced by a dated payload", observed(70, now), &Reading{3, later}, true, observed(3, later)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := reduceWindow(tc.stored, tc.incoming, now, tc.dated)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("window mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestReduceMovesTheSevenDayWindowOnlyOnADatedPayload is the measured case: a
// session that has not had a response since the subscription's schedule changed
// keeps sending the window that schedule had open, and only a payload carrying
// a five-hour window of its own may say which window is open.
func TestReduceMovesTheSevenDayWindowOnlyOnADatedPayload(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-20T13:51:00Z")
	supersededReset := at(t, "2026-09-25T07:00:00Z")
	currentReset := at(t, "2026-09-20T23:00:00Z")
	fiveHourReset := at(t, "2026-09-20T18:10:00Z")

	stale := Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.277", SevenDay: &Reading{7, supersededReset}}
	inUse := Observation{
		CapturedAt:        captured.Add(time.Second),
		ClaudeCodeVersion: "2.1.277",
		FiveHour:          &Reading{2, fiveHourReset},
		SevenDay:          &Reading{78, currentReset},
	}

	state := Reduce(NewState(), inUse)
	state = Reduce(state, stale)

	want := Windows{FiveHour: observed(2, fiveHourReset), SevenDay: observed(78, currentReset)}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
	}
}

// TestReduceRefusesASupersededWindowWhenTheCurrentOneResets is the trap that
// reset order alone walks into: once the current window ends, the window a
// session left behind still holds has days to run. With nothing in use to date
// a payload, the machine reports the window as over rather than adopting it.
func TestReduceRefusesASupersededWindowWhenTheCurrentOneResets(t *testing.T) {
	t.Parallel()
	currentReset := at(t, "2026-09-20T23:00:00Z")
	supersededReset := at(t, "2026-09-25T07:00:00Z")
	afterReset := at(t, "2026-09-20T23:00:01Z")

	state := NewState()
	state.Windows.SevenDay = observed(78, currentReset)
	state.Windows.FiveHour = Window{Status: WindowExpired, ResetsAt: &currentReset}

	state = Reduce(state, Observation{CapturedAt: afterReset, SevenDay: &Reading{7, supersededReset}})

	want := Window{Status: WindowExpired, ResetsAt: &currentReset}
	if diff := cmp.Diff(want, state.Windows.SevenDay); diff != "" {
		t.Fatalf("seven-day mismatch (-want +got):\n%s", diff)
	}
}

// TestReduceTakesTheNewWindowFromTheSessionInUse is the same reset with someone
// working: that session's payload carries a five-hour window, so it may name
// the window that just opened.
func TestReduceTakesTheNewWindowFromTheSessionInUse(t *testing.T) {
	t.Parallel()
	currentReset := at(t, "2026-09-20T23:00:00Z")
	supersededReset := at(t, "2026-09-25T07:00:00Z")
	newReset := at(t, "2026-09-27T23:00:00Z")
	afterReset := at(t, "2026-09-20T23:00:01Z")
	fiveHourReset := at(t, "2026-09-21T03:00:00Z")

	state := NewState()
	state.Windows.SevenDay = observed(78, currentReset)

	state = Reduce(state, Observation{CapturedAt: afterReset, SevenDay: &Reading{7, supersededReset}})
	state = Reduce(state, Observation{
		CapturedAt: afterReset.Add(time.Second),
		FiveHour:   &Reading{1, fiveHourReset},
		SevenDay:   &Reading{2, newReset},
	})

	want := observed(2, newReset)
	if diff := cmp.Diff(want, state.Windows.SevenDay); diff != "" {
		t.Fatalf("seven-day mismatch (-want +got):\n%s", diff)
	}
}

// TestReduceSettlesWhenTwoDatedPayloadsDisagree covers a schedule change seen by
// two sessions that both had a response in the last five hours: the payload
// whose five-hour usage is further along is the later one, and the other cannot
// take the window back, so the state settles instead of alternating.
func TestReduceSettlesWhenTwoDatedPayloadsDisagree(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-20T13:51:00Z")
	fiveHourReset := at(t, "2026-09-20T18:10:00Z")
	beforeChange := at(t, "2026-09-25T07:00:00Z")
	afterChange := at(t, "2026-09-20T23:00:00Z")

	older := Observation{CapturedAt: captured, FiveHour: &Reading{2, fiveHourReset}, SevenDay: &Reading{7, beforeChange}}
	newer := Observation{CapturedAt: captured.Add(time.Second), FiveHour: &Reading{5, fiveHourReset}, SevenDay: &Reading{78, afterChange}}

	state := Reduce(NewState(), older)
	for range 3 {
		state = Reduce(state, newer)
		state = Reduce(state, older)
	}

	want := Windows{FiveHour: observed(5, fiveHourReset), SevenDay: observed(78, afterChange)}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
	}
}

// TestReduceTrustsAnyPayloadUntilAFiveHourWindowIsSeen keeps a subscription
// without a five-hour limit working: with nothing to date a payload by, the
// machine takes what it is given.
func TestReduceTrustsAnyPayloadUntilAFiveHourWindowIsSeen(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-20T13:51:00Z")
	sevenReset := at(t, "2026-09-25T07:00:00Z")

	state := Reduce(NewState(), Observation{CapturedAt: captured, SevenDay: &Reading{7, sevenReset}})

	want := observed(7, sevenReset)
	if diff := cmp.Diff(want, state.Windows.SevenDay); diff != "" {
		t.Fatalf("seven-day mismatch (-want +got):\n%s", diff)
	}
}

func TestReduceRecordsObservationMetadataAndBothWindows(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-17T15:30:00Z")
	fiveReset := at(t, "2026-09-17T16:20:00Z")
	sevenReset := at(t, "2026-09-18T07:00:00Z")
	obs := Observation{
		CapturedAt:        captured,
		ClaudeCodeVersion: "2.1.274",
		FiveHour:          &Reading{UsedPercentage: 24, ResetsAt: fiveReset},
		SevenDay:          &Reading{UsedPercentage: 53, ResetsAt: sevenReset},
	}
	got := Reduce(NewState(), obs)
	want := State{
		SchemaVersion: 1,
		Windows: Windows{
			FiveHour: observed(24, fiveReset),
			SevenDay: observed(53, sevenReset),
		},
		LastObservedAt:    &captured,
		ClaudeCodeVersion: "2.1.274",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestReduceWithOnlySevenDayLeavesFiveHourUnknown(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-17T15:30:00Z")
	sevenReset := at(t, "2026-09-18T07:00:00Z")
	obs := Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274", SevenDay: &Reading{UsedPercentage: 53, ResetsAt: sevenReset}}
	got := Reduce(NewState(), obs)
	want := State{
		SchemaVersion:     1,
		Windows:           Windows{FiveHour: Window{Status: WindowUnknown}, SevenDay: observed(53, sevenReset)},
		LastObservedAt:    &captured,
		ClaudeCodeVersion: "2.1.274",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestReducePreservesLastPublished(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-17T15:30:00Z")
	published := &Published{EventID: "e1", CapturedAt: at(t, "2026-09-17T15:00:00Z"), Windows: Windows{FiveHour: Window{Status: WindowUnknown}, SevenDay: Window{Status: WindowUnknown}}}
	state := NewState()
	state.LastPublished = published
	got := Reduce(state, Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274"})
	if diff := cmp.Diff(published, got.LastPublished); diff != "" {
		t.Fatalf("lastPublished mismatch (-want +got):\n%s", diff)
	}
}
