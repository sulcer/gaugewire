package sink

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	_, foundBefore, errBefore := s.LoadIngestion(t.Context(), "databox-main")
	captured := time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)
	at := captured.Add(5 * time.Second)
	ing := Ingestion{Current: "ing-c", History: "ing-h", CurrentCapturedAt: &captured, At: at}
	errSave := s.SaveIngestion(t.Context(), "databox-main", ing)
	got, found, errAfter := s.LoadIngestion(t.Context(), "databox-main")
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

func TestLoadIngestionReportsACorruptStateFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, store.StateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	_, found, err := StateIngestions{Home: home}.LoadIngestion(t.Context(), "databox-main")
	type outcome struct {
		found, recordCorrupt, stateCorrupt bool
	}
	got := outcome{found, errors.Is(err, ErrIngestionRecordCorrupt), errors.Is(err, store.ErrStateCorrupt)}
	if want := (outcome{false, true, true}); got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

// TestLoadIngestionReportsALockErrorAsRetryable passes a context that is
// already cancelled, so store.Lock fails at once through the caller's context;
// it proves that a lock error comes back as retryable state_lock. The lock is
// held as well to mirror the real case, but the cancelled context decides.
func TestLoadIngestionReportsALockErrorAsRetryable(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("layout: %v", err)
	}
	unlock, held, err := store.TryLock(filepath.Join(home, store.StateLockFile))
	if err != nil || !held {
		t.Fatalf("hold lock: held=%v err=%v", held, err)
	}
	defer func() { _ = unlock() }()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, found, err := StateIngestions{Home: home}.LoadIngestion(ctx, "databox-main")
	class, code := Classify(err)
	type outcome struct {
		failed, found bool
		class         Class
		code          string
	}
	got := outcome{err != nil, found, class, code}
	if want := (outcome{true, false, Retryable, "state_lock"}); got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
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
	if err := s.SaveIngestion(t.Context(), "databox-main", Ingestion{History: "ing-h", At: time.Date(2026, 9, 17, 15, 30, 5, 0, time.UTC)}); err != nil {
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
