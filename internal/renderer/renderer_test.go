package renderer

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"
)

func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh and cat")
	}
}

type outcome struct {
	err    bool
	stdout string
	stderr string
}

func run(t *testing.T, command string, stdin []byte) outcome {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(t.Context(), command, stdin, &stdout, &stderr)
	return outcome{err: err != nil, stdout: stdout.String(), stderr: stderr.String()}
}

func TestRunPassesStdinThroughByteForByte(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	payload := []byte("{\"version\":\"2.1.274\"}\x1b[31m no newline")
	got := run(t, "cat", payload)
	want := outcome{stdout: string(payload)}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunForwardsStderrAndReportsFailure(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	got := run(t, "printf boom >&2; exit 3", nil)
	want := outcome{err: true, stderr: "boom"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunWithoutACommandWritesNothing(t *testing.T) {
	t.Parallel()
	got := run(t, "   ", []byte("payload"))
	want := outcome{}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := Run(ctx, "sleep 5; true", nil, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("err=%v after %s; want an error well before 5 s", err, time.Since(started))
	}
}
