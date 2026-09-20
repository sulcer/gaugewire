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

var ageName = map[age]string{undated: "undated", asRecent: "asRecent", newer: "newer"}

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
		clock    age
		want     Window
	}{
		{"absent and never observed stays unknown", Window{}, nil, newer, Window{Status: WindowUnknown}},
		{"absent and explicitly unknown stays unknown", Window{Status: WindowUnknown}, nil, newer, Window{Status: WindowUnknown}},
		{"first open reading from a payload level with the clock is observed", Window{Status: WindowUnknown}, &Reading{42, future}, asRecent, observed(42, future)},
		{"first open reading from an undated payload is ignored", Window{Status: WindowUnknown}, &Reading{42, future}, undated, Window{Status: WindowUnknown}},
		{"zero percent is a real value", Window{Status: WindowUnknown}, &Reading{0, future}, newer, observed(0, future)},
		{"hundred percent is preserved", Window{Status: WindowUnknown}, &Reading{100, future}, newer, observed(100, future)},
		{"decimal percentage is not rounded", Window{Status: WindowUnknown}, &Reading{41.2, future}, newer, observed(41.2, future)},
		{"absent with an unexpired reset keeps the stored window", observed(42, future), nil, newer, observed(42, future)},
		{"absent with a passed reset expires and keeps the reset for diagnostics", observed(42, past), nil, newer, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired stays expired while absent", Window{Status: WindowExpired, ResetsAt: &past}, nil, newer, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired then an open reading from a payload level with the clock is observed again", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{7, later}, asRecent, observed(7, later)},
		{"expired and an open reading from an undated payload stays expired", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{7, later}, undated, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired and a reading of the window that ended stays expired", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{90, past}, newer, Window{Status: WindowExpired, ResetsAt: &past}},
		{"same reset and lower usage is a session behind the others and is ignored", observed(70, future), &Reading{40, future}, newer, observed(70, future)},
		{"same reset and higher usage is accepted", observed(40, future), &Reading{70, future}, newer, observed(70, future)},
		{"same reset and equal usage is accepted", observed(40, future), &Reading{40, future}, newer, observed(40, future)},
		{"an undated payload still refines the window already stored", observed(40, future), &Reading{70, future}, undated, observed(70, future)},
		{"a reading whose reset has passed is ignored", observed(70, future), &Reading{90, past}, newer, observed(70, future)},
		{"a reading whose reset is exactly now is ignored", observed(70, future), &Reading{90, now}, newer, observed(70, future)},
		{"a reading whose reset has passed cannot start a window", Window{Status: WindowUnknown}, &Reading{90, past}, newer, Window{Status: WindowUnknown}},
		{"the stored window retires when only passed readings arrive", observed(70, past), &Reading{70, past}, newer, Window{Status: WindowExpired, ResetsAt: &past}},
		{"a different open window from a newer payload is adopted", observed(7, later), &Reading{78, future}, newer, observed(78, future)},
		{"taking a window that is still open needs more than a payload level with the clock", observed(7, later), &Reading{78, future}, asRecent, observed(7, later)},
		{"a different open window from an undated payload is ignored", observed(78, future), &Reading{7, later}, undated, observed(78, future)},
		{"a sooner open window from an undated payload is ignored too", observed(2, later), &Reading{7, future}, undated, observed(2, later)},
		{"the window after the stored one reset is taken from a payload level with the clock", observed(70, past), &Reading{3, later}, asRecent, observed(3, later)},
		{"an undated payload cannot name the window after a reset", observed(70, past), &Reading{3, later}, undated, Window{Status: WindowExpired, ResetsAt: &past}},
		{"a stored window whose reset is exactly now is replaced by a payload level with the clock", observed(70, now), &Reading{3, later}, asRecent, observed(3, later)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := reduceWindow(tc.stored, tc.incoming, now, tc.clock)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("window mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPayloadAgeOrdersPayloadsByTheirFiveHourWindow(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-20T13:51:00Z")
	reset := at(t, "2026-09-20T18:10:00Z")
	earlier := at(t, "2026-09-20T15:00:00Z")
	ended := at(t, "2026-09-20T13:00:00Z")

	cases := []struct {
		name     string
		fiveHour Window
		incoming *Reading
		want     age
	}{
		{"no five-hour window and none ever seen is trusted but never newer", Window{Status: WindowUnknown}, nil, asRecent},
		{"no five-hour window once the machine has seen one dates nothing", observed(2, reset), nil, undated},
		{"a five-hour window that has ended dates nothing", observed(2, reset), &Reading{2, ended}, undated},
		{"any open five-hour window is newer than no window at all", Window{Status: WindowUnknown}, &Reading{2, reset}, newer},
		{"a five-hour window that has ended is newer than no window at all", Window{Status: WindowExpired, ResetsAt: &ended}, &Reading{2, reset}, newer},
		{"a later five-hour reset once the stored window has ended is a later window", observed(2, ended), &Reading{1, reset}, newer},
		{"a later five-hour reset while the stored window still runs is a re-anchor", observed(2, earlier), &Reading{1, reset}, undated},
		{"an earlier five-hour reset is an earlier window", observed(2, reset), &Reading{90, earlier}, undated},
		{"more usage in the same five-hour window is later", observed(2, reset), &Reading{5, reset}, newer},
		{"the same usage in the same five-hour window is level", observed(2, reset), &Reading{2, reset}, asRecent},
		{"less usage in the same five-hour window is earlier", observed(5, reset), &Reading{2, reset}, undated},
		{"a five-hour window that has ended while none was ever seen is still trusted", Window{Status: WindowUnknown}, &Reading{2, ended}, asRecent},
		{"a stored window with no values to compare dates every payload", Window{Status: WindowObserved}, &Reading{2, reset}, newer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := payloadAge(tc.fiveHour, Observation{CapturedAt: captured, FiveHour: tc.incoming})
			if got != tc.want {
				t.Fatalf("age mismatch: want %s, got %s", ageName[tc.want], ageName[got])
			}
		})
	}
}

// The measured case: a session with no response since the schedule changed keeps
// sending the window that schedule had open.
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

// The trap reset order alone walks into: once the current window ends, the
// superseded one a session still holds has days to run.
func TestReduceRefusesAnUndatedPayloadWhenTheCurrentWindowResets(t *testing.T) {
	t.Parallel()
	currentReset := at(t, "2026-09-20T23:00:00Z")
	supersededReset := at(t, "2026-09-25T07:00:00Z")
	afterReset := at(t, "2026-09-20T23:00:01Z")

	state := NewState()
	state.Windows.SevenDay = observed(78, currentReset)
	state.Windows.FiveHour = Window{Status: WindowExpired, ResetsAt: &currentReset}

	state = Reduce(state, Observation{CapturedAt: afterReset, SevenDay: &Reading{7, supersededReset}})

	want := Windows{
		FiveHour: Window{Status: WindowExpired, ResetsAt: &currentReset},
		SevenDay: Window{Status: WindowExpired, ResetsAt: &currentReset},
	}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
	}
}

// The same reset with someone working, so a payload carries a five-hour window.
func TestReduceTakesTheNewWindowFromTheSessionInUse(t *testing.T) {
	t.Parallel()
	currentReset := at(t, "2026-09-20T23:00:00Z")
	supersededReset := at(t, "2026-09-25T07:00:00Z")
	newReset := at(t, "2026-09-27T23:00:00Z")
	afterReset := at(t, "2026-09-20T23:00:01Z")
	fiveHourReset := at(t, "2026-09-21T03:00:00Z")

	state := NewState()
	state.Windows.SevenDay = observed(78, currentReset)
	state.Windows.FiveHour = Window{Status: WindowExpired, ResetsAt: &currentReset}

	state = Reduce(state, Observation{CapturedAt: afterReset, SevenDay: &Reading{7, supersededReset}})
	state = Reduce(state, Observation{
		CapturedAt: afterReset.Add(time.Second),
		FiveHour:   &Reading{1, fiveHourReset},
		SevenDay:   &Reading{2, newReset},
	})

	want := Windows{FiveHour: observed(1, fiveHourReset), SevenDay: observed(2, newReset)}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
	}
}

func TestReduceSettlesWhenTwoDatedPayloadsDisagree(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-20T13:51:00Z")
	fiveHourReset := at(t, "2026-09-20T18:10:00Z")
	beforeChange := at(t, "2026-09-25T07:00:00Z")
	afterChange := at(t, "2026-09-20T23:00:00Z")

	behind := Observation{CapturedAt: captured, FiveHour: &Reading{2, fiveHourReset}, SevenDay: &Reading{7, beforeChange}}
	ahead := Observation{CapturedAt: captured.Add(time.Second), FiveHour: &Reading{5, fiveHourReset}, SevenDay: &Reading{78, afterChange}}

	state := Reduce(NewState(), behind)
	for range 3 {
		state = Reduce(state, ahead)
		state = Reduce(state, behind)
	}

	want := Windows{FiveHour: observed(5, fiveHourReset), SevenDay: observed(78, afterChange)}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
	}
}

func TestReduceTrustsAnyPayloadUntilAFiveHourWindowIsSeen(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-20T13:51:00Z")
	sevenReset := at(t, "2026-09-25T07:00:00Z")

	state := Reduce(NewState(), Observation{CapturedAt: captured, SevenDay: &Reading{7, sevenReset}})

	want := Windows{FiveHour: Window{Status: WindowUnknown}, SevenDay: observed(7, sevenReset)}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
	}
}

func TestReduceHoldsTheWindowWhenTwoPayloadsAreLevel(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-20T13:51:00Z")
	fiveHourReset := at(t, "2026-09-20T18:10:00Z")
	beforeChange := at(t, "2026-09-25T07:00:00Z")
	afterChange := at(t, "2026-09-20T23:00:00Z")

	held := Observation{CapturedAt: captured, FiveHour: &Reading{2, fiveHourReset}, SevenDay: &Reading{78, afterChange}}
	rival := Observation{CapturedAt: captured.Add(time.Second), FiveHour: &Reading{2, fiveHourReset}, SevenDay: &Reading{7, beforeChange}}

	state := Reduce(NewState(), held)
	for range 3 {
		state = Reduce(state, held)
		state = Reduce(state, rival)
	}

	want := Windows{FiveHour: observed(2, fiveHourReset), SevenDay: observed(78, afterChange)}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
	}
}

// The cost of the trusting mode: with no five-hour window anywhere, the window
// the machine filled first stands until it ends.
func TestReduceWithoutAFiveHourWindowKeepsTheWindowItHolds(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-20T13:51:00Z")
	sevenReset := at(t, "2026-09-25T07:00:00Z")
	otherReset := at(t, "2026-09-20T23:00:00Z")

	state := Reduce(NewState(), Observation{CapturedAt: captured, SevenDay: &Reading{7, sevenReset}})
	state = Reduce(state, Observation{CapturedAt: captured.Add(time.Second), SevenDay: &Reading{78, otherReset}})

	want := Windows{FiveHour: Window{Status: WindowUnknown}, SevenDay: observed(7, sevenReset)}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
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
