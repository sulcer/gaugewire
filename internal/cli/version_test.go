package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveBuildInfoKeepsLinkerValues(t *testing.T) {
	t.Parallel()
	info := BuildInfo{Version: "v1.0.0", Commit: "abc", Date: "2026-09-17"}
	read := func() (*debug.BuildInfo, bool) {
		t.Error("module build info must not be read when the linker set a version")
		return nil, false
	}
	got := resolveBuildInfo(info, read)
	if got != info {
		t.Fatalf("got %+v, want %+v", got, info)
	}
}

func TestResolveBuildInfoFallsBackToModuleInfo(t *testing.T) {
	t.Parallel()
	read := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v0.1.0"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "deadbeef"},
				{Key: "vcs.time", Value: "2026-09-17T09:00:00Z"},
			},
		}, true
	}
	got := resolveBuildInfo(BuildInfo{Version: "dev", Commit: "none", Date: "unknown"}, read)
	want := BuildInfo{Version: "v0.1.0", Commit: "deadbeef", Date: "2026-09-17T09:00:00Z"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveBuildInfoWithoutModuleInfoKeepsDefaults(t *testing.T) {
	t.Parallel()
	read := func() (*debug.BuildInfo, bool) { return nil, false }
	info := BuildInfo{Version: "dev", Commit: "none", Date: "unknown"}
	got := resolveBuildInfo(info, read)
	if got != info {
		t.Fatalf("got %+v, want %+v", got, info)
	}
}
