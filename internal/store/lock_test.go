package store

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestLockSerialisesWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "state.lock")
	counterPath := filepath.Join(dir, "counter")
	if err := os.WriteFile(counterPath, []byte("0"), 0o600); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := Lock(t.Context(), lockPath, 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = unlock() }()
			raw, err := os.ReadFile(counterPath)
			if err != nil {
				errs <- err
				return
			}
			n, _ := strconv.Atoi(string(raw))
			time.Sleep(time.Millisecond)
			errs <- os.WriteFile(counterPath, []byte(strconv.Itoa(n+1)), 0o600)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("writer failed: %v", err)
		}
	}
	got, _ := os.ReadFile(counterPath)
	if string(got) != strconv.Itoa(writers) {
		t.Fatalf("counter %s, want %d (lost updates)", got, writers)
	}
}

func TestLockTimesOutWhileHeld(t *testing.T) {
	t.Parallel()
	lockPath := filepath.Join(t.TempDir(), "state.lock")
	unlock, err := Lock(t.Context(), lockPath, time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer func() { _ = unlock() }()
	_, err = Lock(t.Context(), lockPath, 50*time.Millisecond)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("got %v, want ErrLockTimeout", err)
	}
}

func TestLockIsReleasedByUnlock(t *testing.T) {
	t.Parallel()
	lockPath := filepath.Join(t.TempDir(), "state.lock")
	unlock, err := Lock(t.Context(), lockPath, time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if unlockErr := unlock(); unlockErr != nil {
		t.Fatalf("unlock: %v", unlockErr)
	}
	second, err := Lock(t.Context(), lockPath, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("second lock after unlock: %v", err)
	}
	_ = second()
}

func TestTryLockReportsAHeldLockWithoutWaiting(t *testing.T) {
	t.Parallel()
	lockPath := filepath.Join(t.TempDir(), "flush.lock")
	unlock, held, err := TryLock(lockPath)
	if err != nil || !held {
		t.Fatalf("first TryLock: held=%v err=%v", held, err)
	}
	defer func() { _ = unlock() }()
	_, heldAgain, err := TryLock(lockPath)
	if err != nil || heldAgain {
		t.Fatalf("second TryLock: held=%v err=%v; want false, nil", heldAgain, err)
	}
}
