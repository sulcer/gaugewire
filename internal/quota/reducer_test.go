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
		want     Window
	}{
		{"absent and never observed stays unknown", Window{}, nil, Window{Status: WindowUnknown}},
		{"absent and explicitly unknown stays unknown", Window{Status: WindowUnknown}, nil, Window{Status: WindowUnknown}},
		{"first valid reading is observed", Window{Status: WindowUnknown}, &Reading{42, future}, observed(42, future)},
		{"zero percent is a real value", Window{Status: WindowUnknown}, &Reading{0, future}, observed(0, future)},
		{"hundred percent is preserved", Window{Status: WindowUnknown}, &Reading{100, future}, observed(100, future)},
		{"decimal percentage is not rounded", Window{Status: WindowUnknown}, &Reading{41.2, future}, observed(41.2, future)},
		{"absent with an unexpired reset keeps the stored window", observed(42, future), nil, observed(42, future)},
		{"absent with a passed reset expires and keeps the reset for diagnostics", observed(42, past), nil, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired stays expired while absent", Window{Status: WindowExpired, ResetsAt: &past}, nil, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired then a fresh reading is observed again", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{7, later}, observed(7, later)},
		{"same reset and lower usage is a stale session and is ignored", observed(70, future), &Reading{40, future}, observed(70, future)},
		{"same reset and higher usage is accepted", observed(40, future), &Reading{70, future}, observed(70, future)},
		{"same reset and equal usage is accepted", observed(40, future), &Reading{40, future}, observed(40, future)},
		{"newer reset with lower usage is a new window and is accepted", observed(70, future), &Reading{3, later}, observed(3, later)},
		{"older reset is a stale session and is ignored", observed(70, later), &Reading{90, future}, observed(70, later)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := reduceWindow(tc.stored, tc.incoming, now)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("window mismatch (-want +got):\n%s", diff)
			}
		})
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
