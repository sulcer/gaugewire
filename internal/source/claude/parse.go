// Package claude turns Claude Code's status-line JSON into a quota observation.
// Only the version and the two subscription windows are read; everything else
// in the payload is ignored and never retained.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

// MinimumVersion is the oldest Claude Code whose status line carries rate limits
// in the documented shape.
const MinimumVersion = "2.1.251"

var (
	// ErrUnsupportedVersion means the payload's version is missing, unparseable
	// or older than MinimumVersion; the observation must be skipped.
	ErrUnsupportedVersion = errors.New("claude code version is missing or older than " + MinimumVersion)
	// ErrInvalidPayload means stdin did not hold a JSON object.
	ErrInvalidPayload = errors.New("status-line payload is not a JSON object")
)

type payload struct {
	Version    string `json:"version"`
	RateLimits struct {
		FiveHour json.RawMessage `json:"five_hour"`
		SevenDay json.RawMessage `json:"seven_day"`
	} `json:"rate_limits"`
}

type rawWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
}

// Parse reads one status-line payload. It returns the observation, the JSON
// paths of windows that were present but invalid (treated as absent), and an
// error when the payload is unusable as a whole.
func Parse(r io.Reader, capturedAt time.Time) (quota.Observation, []string, error) {
	var p payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return quota.Observation{}, nil, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	if !versionAtLeast(p.Version, MinimumVersion) {
		return quota.Observation{}, nil, ErrUnsupportedVersion
	}
	obs := quota.Observation{CapturedAt: capturedAt.UTC(), ClaudeCodeVersion: p.Version}
	var issues []string
	obs.FiveHour, issues = parseWindow(p.RateLimits.FiveHour, "rate_limits.five_hour", issues)
	obs.SevenDay, issues = parseWindow(p.RateLimits.SevenDay, "rate_limits.seven_day", issues)
	return obs, issues, nil
}

// parseWindow returns nil for an absent window and nil plus an issue for an
// invalid one. Absent is never zero.
func parseWindow(raw json.RawMessage, path string, issues []string) (*quota.Reading, []string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, issues
	}
	var w rawWindow
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, append(issues, path)
	}
	if w.UsedPercentage == nil || w.ResetsAt == nil {
		return nil, append(issues, path)
	}
	if *w.UsedPercentage < 0 || *w.UsedPercentage > 100 || *w.ResetsAt <= 0 {
		return nil, append(issues, path)
	}
	return &quota.Reading{UsedPercentage: *w.UsedPercentage, ResetsAt: time.Unix(*w.ResetsAt, 0).UTC()}, issues
}

// versionAtLeast compares dotted numeric versions such as "2.1.274". Anything
// that is not three non-negative integers is treated as unsupported.
func versionAtLeast(version, minimum string) bool {
	have, ok := parseVersion(version)
	if !ok {
		return false
	}
	want, _ := parseVersion(minimum)
	for i := range 3 {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return true
}

func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
