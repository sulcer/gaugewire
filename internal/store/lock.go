package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/flock"
)

// ErrLockTimeout means the lock was still held when the wait budget ran out.
var ErrLockTimeout = errors.New("lock wait timed out")

// Unlock releases a lock obtained from Lock or TryLock.
type Unlock func() error

const lockRetryDelay = 10 * time.Millisecond

// Lock acquires the advisory file lock at path, waiting at most timeout.
func Lock(ctx context.Context, path string, timeout time.Duration) (Unlock, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lock := flock.New(path)
	locked, err := lock.TryLockContext(waitCtx, lockRetryDelay)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !locked {
		return nil, fmt.Errorf("%w: %s", ErrLockTimeout, path)
	}
	return lock.Unlock, nil
}

// TryLock acquires the lock at path without waiting. held is false when another
// process holds it.
func TryLock(path string) (unlock Unlock, held bool, err error) {
	lock := flock.New(path)
	held, err = lock.TryLock()
	if err != nil {
		return nil, false, fmt.Errorf("try lock %s: %w", path, err)
	}
	if !held {
		return nil, false, nil
	}
	return lock.Unlock, true, nil
}
