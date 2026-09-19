package sink

import (
	"testing"
	"time"
)

func TestNextAttemptFollowsTheSchedule(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	noJitter := func() float64 { return 0.5 }
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, 5 * time.Second},
		{2, 30 * time.Second},
		{3, 2 * time.Minute},
		{4, 10 * time.Minute},
		{5, 30 * time.Minute},
		{9, 30 * time.Minute},
		{0, 5 * time.Second},
	}
	for _, tc := range cases {
		got := NextAttempt(tc.attempts, now, noJitter).Sub(now)
		if got != tc.want {
			t.Fatalf("attempt %d: got %s, want %s", tc.attempts, got, tc.want)
		}
	}
}

func TestNextAttemptJitterStaysWithinTwentyPercent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	low := NextAttempt(2, now, func() float64 { return 0 }).Sub(now)
	high := NextAttempt(2, now, func() float64 { return 0.999999 }).Sub(now)
	if low != 24*time.Second || high < 35*time.Second || high >= 36*time.Second {
		t.Fatalf("low=%s high=%s; want 24s and just under 36s", low, high)
	}
}
