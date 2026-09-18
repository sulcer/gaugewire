package quota

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMarkPublishedSnapshotsTheCurrentWindows(t *testing.T) {
	t.Parallel()
	reset := at(t, "2026-09-17T18:00:00Z")
	captured := at(t, "2026-09-17T15:30:00Z")
	state := NewState()
	state.Windows = Windows{FiveHour: observed(24, reset), SevenDay: Window{Status: WindowUnknown}}
	got := MarkPublished(state, "event-1", captured)
	want := &Published{EventID: "event-1", CapturedAt: captured, Windows: Windows{FiveHour: observed(24, reset), SevenDay: Window{Status: WindowUnknown}}}
	if diff := cmp.Diff(want, got.LastPublished); diff != "" {
		t.Fatalf("lastPublished mismatch (-want +got):\n%s", diff)
	}
}
