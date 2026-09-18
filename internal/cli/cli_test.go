package cli

import (
	"bytes"
	"testing"
)

// outcome captures everything a command produces, so each test compares one value.
type outcome struct {
	err    string
	stdout string
}

func runCommand(t *testing.T, args []string, info BuildInfo) outcome {
	t.Helper()
	var stdout bytes.Buffer
	err := Run(t.Context(), args, info, IO{Stdout: &stdout})
	got := outcome{stdout: stdout.String()}
	if err != nil {
		got.err = err.Error()
	}
	return got
}

func TestVersionWritesBuildInfo(t *testing.T) {
	t.Parallel()
	info := BuildInfo{Version: "v1.2.3", Commit: "abc1234", Date: "2026-09-17T10:00:00Z"}
	got := runCommand(t, []string{"version"}, info)
	want := outcome{stdout: "gaugewire v1.2.3\ncommit: abc1234\nbuilt:  2026-09-17T10:00:00Z\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNoArgumentsReturnsUsage(t *testing.T) {
	t.Parallel()
	got := runCommand(t, nil, BuildInfo{})
	want := outcome{err: ErrUsage.Error()}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUnknownCommandReturnsUsage(t *testing.T) {
	t.Parallel()
	got := runCommand(t, []string{"bogus"}, BuildInfo{})
	want := outcome{err: `unknown command "bogus": ` + ErrUsage.Error()}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
