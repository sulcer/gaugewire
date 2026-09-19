package sink

import "time"

// schedule is the retry backoff by attempt number; the last entry repeats forever.
var schedule = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

const jitterFraction = 0.2

// NextAttempt returns when a delivery should be retried after the given number
// of failed attempts (1 for the first failure), with ±20 % jitter drawn from
// random, which must return a value in [0, 1).
func NextAttempt(attempts int, now time.Time, random func() float64) time.Time {
	index := attempts - 1
	if index < 0 {
		index = 0
	}
	if index >= len(schedule) {
		index = len(schedule) - 1
	}
	base := schedule[index]
	jitter := time.Duration((random()*2 - 1) * jitterFraction * float64(base))
	return now.Add(base + jitter)
}
