// Package quota is Gaugewire's pure domain: rate-limit windows, the reducer, the
// publish decision and the QuotaSnapshot v1 contract. It does no I/O.
package quota

import "time"

// WindowStatus says what is known about one rate-limit window.
type WindowStatus string

const (
	// WindowUnknown means the window has never been observed.
	WindowUnknown WindowStatus = "unknown"
	// WindowObserved means a valid value is held.
	WindowObserved WindowStatus = "observed"
	// WindowExpired means the stored reset time passed and no fresh value arrived.
	WindowExpired WindowStatus = "expired"
)

// Window is the stored state of one rate-limit window. UsedPercentage is nil
// unless the status is observed; ResetsAt is kept through expiry for diagnostics.
type Window struct {
	Status         WindowStatus `json:"status"`
	UsedPercentage *float64     `json:"usedPercentage"`
	ResetsAt       *time.Time   `json:"resetsAt"`
}

// Windows holds both subscription windows.
type Windows struct {
	FiveHour Window `json:"fiveHour"`
	SevenDay Window `json:"sevenDay"`
}

// Reading is one valid window value reported by a source.
type Reading struct {
	UsedPercentage float64
	ResetsAt       time.Time
}
