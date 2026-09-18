package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

func golden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	return string(data)
}

func TestRenderStatusObserved(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, 9, 17, 16, 32, 0, 0, time.UTC) // 18:32 CEST
	captured := now.Add(-2 * time.Minute)
	used5, used7 := 24.0, 53.5
	reset5 := time.Date(2026, 9, 17, 16, 20, 0, 0, time.UTC) // 18:20 CEST, today
	reset7 := time.Date(2026, 9, 18, 7, 0, 0, 0, time.UTC)   // Sep 18 09:00 CEST
	state := store.NewState()
	state.Windows = quota.Windows{
		FiveHour: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used5, ResetsAt: &reset5},
		SevenDay: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used7, ResetsAt: &reset7},
	}
	state.LastObservedAt = &captured
	state.LastPublished = &quota.Published{EventID: "e", CapturedAt: captured, Windows: state.Windows}
	state.LastFlush = &store.FlushRecord{At: now.Add(-time.Minute), OK: true}
	got := renderStatus(testConfig(databoxSink()), state, 1, 0, "", now, zone)
	if want := golden(t, "status_observed.golden"); got != want {
		t.Fatalf("status mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderStatusFresh(t *testing.T) {
	t.Parallel()
	got := renderStatus(testConfig(databoxSink()), store.NewState(), 0, 0, "", time.Date(2026, 9, 17, 16, 32, 0, 0, time.UTC), time.UTC)
	if want := golden(t, "status_fresh.golden"); got != want {
		t.Fatalf("status mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderStatusShowsTheNewestDeadLetterReason(t *testing.T) {
	t.Parallel()
	reason := "databox-main: permanent (invalid_api_key): 401"
	got := renderStatus(testConfig(databoxSink()), store.NewState(), 0, 1, reason, time.Date(2026, 9, 17, 16, 32, 0, 0, time.UTC), time.UTC)
	if want := golden(t, "status_deadletters.golden"); got != want {
		t.Fatalf("status mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunStatusShowsAFreshStateWhenStateIsCorrupt(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	if err := config.Save(home, testConfig(databoxSink())); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, store.StateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout bytes.Buffer
	err := runStatus(&stdout, time.Date(2026, 9, 17, 16, 32, 0, 0, time.UTC), time.UTC)

	got := struct {
		err bool
		out string
	}{err != nil, stdout.String()}
	want := struct {
		err bool
		out string
	}{false, "state.json is not valid; showing a fresh state\n" + golden(t, "status_fresh.golden")}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunStatusSurvivesAnUnreadableDeadLetter(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	if err := store.EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	if err := config.Save(home, testConfig(databoxSink())); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, store.DeadLetterDir, "1789657000000-evt-bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var stdout bytes.Buffer
	err := runStatus(&stdout, time.Date(2026, 9, 17, 16, 32, 0, 0, time.UTC), time.UTC)

	got := struct {
		err bool
		out string
	}{err != nil, stdout.String()}
	want := struct {
		err bool
		out string
	}{false, "dead-letter/ could not be read; newest reason unavailable\n" +
		strings.Replace(golden(t, "status_fresh.golden"), "Dead letters:      0", "Dead letters:      1", 1)}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
