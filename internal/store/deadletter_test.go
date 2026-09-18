package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestListDeadLettersNewestFirstAndCountsUnreadable(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	for _, id := range []string{"evt-a", "evt-b"} {
		if _, err := WritePending(home, event(id, captured)); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
		captured = captured.Add(time.Minute)
	}
	pending, err := ListPending(home)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if err = DeadLetter(home, pending[0], "databox-main: permanent (invalid_api_key): 401", captured.Add(time.Hour)); err != nil {
		t.Fatalf("dead-letter a: %v", err)
	}
	if err = DeadLetter(home, pending[1], "databox-main: permanent (forbidden): 403", captured.Add(2*time.Hour)); err != nil {
		t.Fatalf("dead-letter b: %v", err)
	}
	if err = os.WriteFile(filepath.Join(home, DeadLetterDir, "1789657000000-evt-bad.json.unreadable"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write unreadable: %v", err)
	}
	entries, unreadable, err := ListDeadLetters(home)
	type outcome struct {
		entries    []DeadLetterEntry
		unreadable int
		err        bool
	}
	got := outcome{entries, unreadable, err != nil}
	want := outcome{
		entries: []DeadLetterEntry{
			{EventID: "evt-b", Reason: "databox-main: permanent (forbidden): 403", DeadLetteredAt: captured.Add(2 * time.Hour)},
			{EventID: "evt-a", Reason: "databox-main: permanent (invalid_api_key): 401", DeadLetteredAt: captured.Add(time.Hour)},
		},
		unreadable: 1,
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestListDeadLettersOnAnEmptyHome(t *testing.T) {
	t.Parallel()
	entries, unreadable, err := ListDeadLetters(t.TempDir())
	if err != nil || unreadable != 0 || len(entries) != 0 {
		t.Fatalf("got %v %d %v, want nothing", entries, unreadable, err)
	}
}
