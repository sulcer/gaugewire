package sink

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/store"
)

func TestStateIngestionsRoundTrip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	s := StateIngestions{Home: home}
	_, foundBefore, errBefore := s.LoadIngestion("databox-main")
	captured := time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)
	at := captured.Add(5 * time.Second)
	ing := Ingestion{Current: "ing-c", History: "ing-h", CurrentCapturedAt: &captured, At: at}
	errSave := s.SaveIngestion("databox-main", ing)
	got, found, errAfter := s.LoadIngestion("databox-main")
	state, _ := store.LoadState(home)
	type outcome struct {
		foundBefore, found bool
		errs               [3]bool
		ing                Ingestion
		stored             store.IngestionRecord
	}
	o := outcome{foundBefore, found, [3]bool{errBefore != nil, errSave != nil, errAfter != nil}, got, state.LastIngestion["databox-main"]}
	want := outcome{false, true, [3]bool{}, ing, store.IngestionRecord{Current: "ing-c", History: "ing-h", CurrentCapturedAt: &captured, At: at}}
	if diff := cmp.Diff(want, o, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestSaveIngestionKeepsOtherSinksAndQuotaState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	state := store.NewState()
	state.ClaudeCodeVersion = "2.1.274"
	state.LastIngestion = map[string]store.IngestionRecord{"other": {History: "keep"}}
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save: %v", err)
	}
	s := StateIngestions{Home: home}
	if err := s.SaveIngestion("databox-main", Ingestion{History: "ing-h", At: time.Date(2026, 9, 17, 15, 30, 5, 0, time.UTC)}); err != nil {
		t.Fatalf("SaveIngestion: %v", err)
	}
	after, _ := store.LoadState(home)
	got := struct {
		version string
		other   string
		main    string
	}{after.ClaudeCodeVersion, after.LastIngestion["other"].History, after.LastIngestion["databox-main"].History}
	want := struct {
		version string
		other   string
		main    string
	}{"2.1.274", "keep", "ing-h"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
