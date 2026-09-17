package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/quota"
)

var captured = time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "fixtures", "statusline", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func epoch(seconds int64) time.Time { return time.Unix(seconds, 0).UTC() }

type parsed struct {
	obs    quota.Observation
	issues []string
	err    error
}

func TestParseFixtures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fixture string
		want    parsed
	}{
		{"full.json", parsed{
			obs: quota.Observation{
				CapturedAt: captured, ClaudeCodeVersion: "2.1.274",
				FiveHour: &quota.Reading{UsedPercentage: 23.5, ResetsAt: epoch(1789659600)},
				SevenDay: &quota.Reading{UsedPercentage: 41.2, ResetsAt: epoch(1789714800)},
			},
		}},
		{"no-rate-limits.json", parsed{obs: quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274"}}},
		{"five-hour-only.json", parsed{obs: quota.Observation{
			CapturedAt: captured, ClaudeCodeVersion: "2.1.274",
			FiveHour: &quota.Reading{UsedPercentage: 24, ResetsAt: epoch(1789659600)},
		}}},
		{"seven-day-only.json", parsed{obs: quota.Observation{
			CapturedAt: captured, ClaudeCodeVersion: "2.1.274",
			SevenDay: &quota.Reading{UsedPercentage: 53, ResetsAt: epoch(1789714800)},
		}}},
		{"zero-and-hundred.json", parsed{obs: quota.Observation{
			CapturedAt: captured, ClaudeCodeVersion: "2.1.274",
			FiveHour: &quota.Reading{UsedPercentage: 0, ResetsAt: epoch(1789659600)},
			SevenDay: &quota.Reading{UsedPercentage: 100, ResetsAt: epoch(1789714800)},
		}}},
		{"malformed-percentage.json", parsed{
			obs:    quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274"},
			issues: []string{"rate_limits.five_hour", "rate_limits.seven_day"},
		}},
		{"malformed-reset.json", parsed{
			obs:    quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274"},
			issues: []string{"rate_limits.five_hour", "rate_limits.seven_day"},
		}},
		{"old-version.json", parsed{err: ErrUnsupportedVersion}},
		{"missing-version.json", parsed{err: ErrUnsupportedVersion}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			obs, issues, err := Parse(fixture(t, tc.fixture), captured)
			got := parsed{obs: obs, issues: issues, err: err}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(parsed{}), cmp.Comparer(func(a, b error) bool { return errors.Is(a, b) || errors.Is(b, a) })); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseRejectsNonJSON(t *testing.T) {
	t.Parallel()
	_, _, err := Parse(strings.NewReader("not json"), captured)
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("got %v, want ErrInvalidPayload", err)
	}
}

func TestParseRejectsEmptyInput(t *testing.T) {
	t.Parallel()
	_, _, err := Parse(strings.NewReader(""), captured)
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("got %v, want ErrInvalidPayload", err)
	}
}

func TestParseAcceptsTheMinimumVersion(t *testing.T) {
	t.Parallel()
	obs, _, err := Parse(strings.NewReader(`{"version":"2.1.251"}`), captured)
	got := parsed{obs: obs, err: err}
	want := parsed{obs: quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.251"}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(parsed{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestParseTreatsAnUnparseableVersionAsUnsupported(t *testing.T) {
	t.Parallel()
	_, _, err := Parse(strings.NewReader(`{"version":"nightly"}`), captured)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("got %v, want ErrUnsupportedVersion", err)
	}
}
