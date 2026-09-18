package quota

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSnapshotJSONMatchesTheContract(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-17T15:30:00Z")
	state := NewState()
	state.Windows = Windows{
		FiveHour: observed(24, at(t, "2026-09-17T16:20:00Z")),
		SevenDay: observed(53.5, at(t, "2026-09-18T07:00:00Z")),
	}
	state.ClaudeCodeVersion = "2.1.274"
	identity := Identity{NodeID: "node-uuid", NodeAlias: "mac-mini-01", Platform: "darwin", AccountID: "account-uuid", AccountAlias: "claude-01", ObserverVersion: "1.0.0"}
	got, err := json.Marshal(NewSnapshot(identity, state, "event-uuid", captured))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"schemaVersion":1,"eventId":"event-uuid",` +
		`"node":{"id":"node-uuid","alias":"mac-mini-01","platform":"darwin"},` +
		`"account":{"id":"account-uuid","alias":"claude-01"},` +
		`"capturedAt":"2026-09-17T15:30:00Z",` +
		`"windows":{"fiveHour":{"status":"observed","usedPercentage":24,"resetsAt":"2026-09-17T16:20:00Z"},` +
		`"sevenDay":{"status":"observed","usedPercentage":53.5,"resetsAt":"2026-09-18T07:00:00Z"}},` +
		`"source":{"type":"claude-code-statusline","claudeCodeVersion":"2.1.274"},` +
		`"observerVersion":"1.0.0"}`
	if string(got) != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestSnapshotNormalisesCapturedAtToUTCMilliseconds(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("CEST", 2*60*60)
	captured := time.Date(2026, 9, 17, 17, 30, 0, 123_456_789, zone)
	got, err := json.Marshal(NewSnapshot(Identity{}, NewState(), "e", captured).CapturedAt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `"2026-09-17T15:30:00.123Z"`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestSnapshotJSONKeepsUnknownWindowsAsNulls(t *testing.T) {
	t.Parallel()
	state := NewState()
	got, err := json.Marshal(NewSnapshot(Identity{}, state, "e", at(t, "2026-09-17T15:30:00Z")).Windows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"fiveHour":{"status":"unknown","usedPercentage":null,"resetsAt":null},` +
		`"sevenDay":{"status":"unknown","usedPercentage":null,"resetsAt":null}}`
	if string(got) != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", got, want)
	}
}
