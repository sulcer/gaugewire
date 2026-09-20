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
		recent   bool
		want     Window
	}{
		{"absent and never observed stays unknown", Window{}, nil, true, Window{Status: WindowUnknown}},
		{"absent and explicitly unknown stays unknown", Window{Status: WindowUnknown}, nil, true, Window{Status: WindowUnknown}},
		{"first open reading is observed", Window{Status: WindowUnknown}, &Reading{42, future}, true, observed(42, future)},
		{"zero percent is a real value", Window{Status: WindowUnknown}, &Reading{0, future}, true, observed(0, future)},
		{"hundred percent is preserved", Window{Status: WindowUnknown}, &Reading{100, future}, true, observed(100, future)},
		{"decimal percentage is not rounded", Window{Status: WindowUnknown}, &Reading{41.2, future}, true, observed(41.2, future)},
		{"absent with an unexpired reset keeps the stored window", observed(42, future), nil, true, observed(42, future)},
		{"absent with a passed reset expires and keeps the reset for diagnostics", observed(42, past), nil, true, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired stays expired while absent", Window{Status: WindowExpired, ResetsAt: &past}, nil, true, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired then an open reading is observed again", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{7, later}, true, observed(7, later)},
		{"expired and a reading of the window that ended stays expired", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{90, past}, true, Window{Status: WindowExpired, ResetsAt: &past}},
		{"same reset and lower usage is a session behind the others and is ignored", observed(70, future), &Reading{40, future}, true, observed(70, future)},
		{"same reset and higher usage is accepted", observed(40, future), &Reading{70, future}, true, observed(70, future)},
		{"same reset and equal usage is accepted", observed(40, future), &Reading{40, future}, true, observed(40, future)},
		{"a session behind the others still refines the same window", observed(40, future), &Reading{70, future}, false, observed(70, future)},
		{"a reading whose reset has passed is ignored", observed(70, future), &Reading{90, past}, true, observed(70, future)},
		{"a reading whose reset is exactly now is ignored", observed(70, future), &Reading{90, now}, true, observed(70, future)},
		{"a reading whose reset has passed cannot start a window", Window{Status: WindowUnknown}, &Reading{90, past}, true, Window{Status: WindowUnknown}},
		{"the stored window retires when only passed readings arrive", observed(70, past), &Reading{70, past}, true, Window{Status: WindowExpired, ResetsAt: &past}},
		{"a different open window from a payload of the last five hours is adopted", observed(7, later), &Reading{78, future}, true, observed(78, future)},
		{"a different open window from an older payload is ignored", observed(78, future), &Reading{7, later}, false, observed(78, future)},
		{"a sooner open window from an older payload is ignored too", observed(2, later), &Reading{7, future}, false, observed(2, later)},
		{"a new window after the stored one reset is accepted from any payload", observed(70, past), &Reading{3, later}, false, observed(3, later)},
		{"a stored window whose reset is exactly now is replaced", observed(70, now), &Reading{3, later}, false, observed(3, later)},
		{"an older payload fills a window never seen", Window{Status: WindowUnknown}, &Reading{7, later}, false, observed(7, later)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := reduceWindow(tc.stored, tc.incoming, now, tc.recent)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("window mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestReduceMovesTheSevenDayWindowOnlyOnARecentPayload is the measured case: a
// session that has not had a response since the subscription's schedule changed
// keeps sending the window that schedule had open, and only a payload whose
// five-hour window is still running may move the seven-day window.
func TestReduceMovesTheSevenDayWindowOnlyOnARecentPayload(t *testing.T) {
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

	state := Reduce(NewState(), stale)
	state = Reduce(state, inUse)
	state = Reduce(state, stale)

	want := Windows{FiveHour: observed(2, fiveHourReset), SevenDay: observed(78, currentReset)}
	if diff := cmp.Diff(want, state.Windows); diff != "" {
		t.Fatalf("windows mismatch (-want +got):\n%s", diff)
	}
}

// TestReduceIgnoresASupersededWindowAfterTheCurrentOneResets keeps the trap
// that reset order alone would walk into: once the current window ends, the
// window a stale session still holds has time left, and a payload that old
// must not be allowed to name it.
func TestReduceIgnoresASupersededWindowAfterTheCurrentOneResets(t *testing.T) {
	t.Parallel()
	supersededReset := at(t, "2026-09-25T07:00:00Z")
	newReset := at(t, "2026-09-27T23:00:00Z")
	afterReset := at(t, "2026-09-20T23:00:01Z")
	fiveHourReset := at(t, "2026-09-21T03:00:00Z")

	state := Reduce(NewState(), Observation{
		CapturedAt: afterReset,
		FiveHour:   &Reading{1, fiveHourReset},
		SevenDay:   &Reading{2, newReset},
	})
	state = Reduce(state, Observation{CapturedAt: afterReset.Add(time.Second), SevenDay: &Reading{7, supersededReset}})

	want := observed(2, newReset)
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
