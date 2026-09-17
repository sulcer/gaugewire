package cli

import (
	"fmt"
	"io"
	"runtime/debug"
)

func runVersion(info BuildInfo, stdout io.Writer) error {
	resolved := resolveBuildInfo(info, debug.ReadBuildInfo)
	_, err := fmt.Fprintf(stdout, "gaugewire %s\ncommit: %s\nbuilt:  %s\n", resolved.Version, resolved.Commit, resolved.Date)
	return err
}

// resolveBuildInfo fills in values the linker did not set from the module build
// info, so a binary built with `go build` still reports a useful version.
func resolveBuildInfo(info BuildInfo, read func() (*debug.BuildInfo, bool)) BuildInfo {
	if info.Version != "dev" {
		return info
	}
	buildInfo, ok := read()
	if !ok {
		return info
	}
	if buildInfo.Main.Version != "" && buildInfo.Main.Version != "(devel)" {
		info.Version = buildInfo.Main.Version
	}
	for _, setting := range buildInfo.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Commit = setting.Value
		case "vcs.time":
			info.Date = setting.Value
		}
	}
	return info
}
