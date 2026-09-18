// Package sink defines the destination interface, the error classes a sink
// reports, the retry schedule, and the flusher that delivers spooled events.
package sink

import (
	"context"
	"errors"

	"github.com/sulcer/gaugewire/internal/quota"
)

// MaxBatch is the largest number of snapshots handed to a sink in one call.
const MaxBatch = 100

// Sink is a destination for snapshots. Implementations classify failures with
// NewRetryable and NewPermanent so the flusher can decide what to do.
type Sink interface {
	ID() string
	PublishBatch(ctx context.Context, snapshots []quota.Snapshot) error
}

// Class says whether a failed delivery should be retried or dead-lettered.
type Class int

const (
	// Retryable failures are transient: network, timeouts, 5xx, throttling.
	Retryable Class = iota
	// Permanent failures need a human: credentials, schema, configuration.
	Permanent
)

// Error is a classified delivery failure.
type Error struct {
	Class Class
	Code  string
	Err   error
}

func (e *Error) Error() string {
	class := "retryable"
	if e.Class == Permanent {
		class = "permanent"
	}
	if e.Code == "" {
		return class + ": " + e.Err.Error()
	}
	return class + " (" + e.Code + "): " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// NewRetryable wraps err as a transient failure with an optional code.
func NewRetryable(code string, err error) error {
	return &Error{Class: Retryable, Code: code, Err: err}
}

// NewPermanent wraps err as a failure that must not be retried.
func NewPermanent(code string, err error) error {
	return &Error{Class: Permanent, Code: code, Err: err}
}

// Classify returns the class and code of a delivery error. An error a sink did
// not classify is treated as retryable, so nothing is ever dropped by accident.
func Classify(err error) (Class, string) {
	if sinkErr, ok := errors.AsType[*Error](err); ok {
		return sinkErr.Class, sinkErr.Code
	}
	return Retryable, ""
}
