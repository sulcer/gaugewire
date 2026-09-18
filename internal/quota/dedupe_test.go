package quota

import (
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	t.Parallel()
	reset := at(t, "2026-09-17T18:00:00Z")
	nextReset := at(t, "2026-09-17T23:00:00Z")
	publishedAt := at(t, "2026-09-17T15:00:00Z")
	soon := at(t, "2026-09-17T15:10:00Z")
	late := at(t, "2026-09-17T15:31:00Z")
	exactly := at(t, "2026-09-17T15:30:00Z")
	unknownWindows := Windows{FiveHour: Window{Status: WindowUnknown}, SevenDay: Window{Status: WindowUnknown}}
	published := func(w Windows) *Published {
		return &Published{EventID: "e1", CapturedAt: publishedAt, Windows: w}
	}
	withPublished := func(current Windows, last *Published) State {
		s := NewState()
		s.Windows = current
		s.LastPublished = last
		return s
	}
	both := func(five, seven float64) Windows {
		return Windows{FiveHour: observed(five, reset), SevenDay: observed(seven, reset)}
	}

	cases := []struct {
		name  string
		state State
		now   time.Time
		want  Decision
	}{
		{"never published and nothing observed does not publish", withPublished(unknownWindows, nil), soon, Decision{}},
		{"never published and one window observed publishes a state transition", withPublished(Windows{FiveHour: observed(24, reset), SevenDay: Window{Status: WindowUnknown}}, nil), soon, Decision{Publish: true, EventType: EventStateTransition}},
		{"a window status change publishes a state transition", withPublished(Windows{FiveHour: Window{Status: WindowExpired, ResetsAt: &reset}, SevenDay: observed(53, reset)}, published(both(40, 53))), soon, Decision{Publish: true, EventType: EventStateTransition}},
		{"a reset change publishes a change", withPublished(Windows{FiveHour: observed(2, nextReset), SevenDay: observed(53, reset)}, published(both(40, 53))), soon, Decision{Publish: true, EventType: EventChange}},
		{"a delta below the threshold does not publish", withPublished(both(40.7, 53), published(both(40, 53))), soon, Decision{}},
		{"a delta at or above the threshold publishes a change", withPublished(both(41.1, 53), published(both(40, 53))), soon, Decision{Publish: true, EventType: EventChange}},
		{"a drop of the threshold in the seven day window publishes a change", withPublished(both(40, 51.9), published(both(40, 53))), soon, Decision{Publish: true, EventType: EventChange}},
		{"a one point move that straddles a power of two publishes a change", withPublished(both(64.1, 53), published(both(63.1, 53))), soon, Decision{Publish: true, EventType: EventChange}},
		{"a one point drop that straddles a power of two publishes a change", withPublished(both(40, 31.3), published(both(40, 32.3))), soon, Decision{Publish: true, EventType: EventChange}},
		{"nothing changed before the heartbeat interval does not publish", withPublished(both(40, 53), published(both(40, 53))), soon, Decision{}},
		{"nothing changed after the heartbeat interval publishes a heartbeat", withPublished(both(40, 53), published(both(40, 53))), late, Decision{Publish: true, EventType: EventHeartbeat}},
		{"nothing changed at exactly the heartbeat interval publishes a heartbeat", withPublished(both(40, 53), published(both(40, 53))), exactly, Decision{Publish: true, EventType: EventHeartbeat}},
		{"heartbeat needs an observed window", withPublished(Windows{FiveHour: Window{Status: WindowExpired, ResetsAt: &reset}, SevenDay: Window{Status: WindowExpired, ResetsAt: &reset}}, published(Windows{FiveHour: Window{Status: WindowExpired, ResetsAt: &reset}, SevenDay: Window{Status: WindowExpired, ResetsAt: &reset}})), late, Decision{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Decide(tc.state, tc.now, DefaultPublishing())
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecideComparesAgainstLastPublishedNotPreviousInput(t *testing.T) {
	t.Parallel()
	reset := at(t, "2026-09-17T18:00:00Z")
	last := &Published{EventID: "e1", CapturedAt: at(t, "2026-09-17T15:00:00Z"), Windows: Windows{FiveHour: observed(40, reset), SevenDay: observed(53, reset)}}
	state := NewState()
	state.LastPublished = last
	// Three small steps that each stay under the threshold relative to the
	// previous input but accumulate past it relative to the last published value.
	steps := []float64{40.2, 40.4, 40.7, 41.1}
	got := make([]bool, 0, len(steps))
	for _, used := range steps {
		state.Windows = Windows{FiveHour: observed(used, reset), SevenDay: observed(53, reset)}
		got = append(got, Decide(state, at(t, "2026-09-17T15:05:00Z"), DefaultPublishing()).Publish)
	}
	want := []bool{false, false, false, true}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("publish sequence %v, want %v", got, want)
	}
}
