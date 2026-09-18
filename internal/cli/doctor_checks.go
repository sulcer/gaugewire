package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"uuid"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/settings"
	"github.com/sulcer/gaugewire/internal/source/claude"
	"github.com/sulcer/gaugewire/internal/store"
)

func checkConfiguration(err error) check {
	if err != nil {
		return check{name: "configuration", detail: err.Error()}
	}
	return check{name: "configuration", ok: true, detail: "valid"}
}

func checkClaudeCodeVersion(state store.State) check {
	if state.ClaudeCodeVersion == "" {
		return check{name: "claude code version", ok: true, detail: "no observation yet"}
	}
	detail := state.ClaudeCodeVersion + " (minimum " + claude.MinimumVersion + ")"
	return check{name: "claude code version", ok: claude.VersionSupported(state.ClaudeCodeVersion), detail: detail}
}

func checkStatusLineIntegration(cfg config.Config, in doctorInput) check {
	name := "status-line integration"
	if cfg.Install == nil {
		return check{name: name, detail: "not installed"}
	}
	file, _, err := settings.Load(in.settingsPath)
	if err != nil {
		return check{name: name, detail: err.Error()}
	}
	member, err := settings.Get(file, "statusLine")
	if err != nil {
		return check{name: name, detail: err.Error()}
	}
	command := commandOf(member.Value)
	switch {
	case command == "":
		return check{name: name, detail: "not installed"}
	case command != cfg.Install.InstalledCommand:
		return check{name: name, detail: "points elsewhere: " + command}
	}
	return check{name: name, ok: true, detail: command}
}

// checkOverrides looks for project settings that replace the status line or
// disable hooks, both of which stop the installed command from running. The
// files are read in Claude Code's precedence order, local before project before
// user, so the file named is the one that actually wins.
func checkOverrides(in doctorInput) check {
	name := "overrides"
	projectLocal := filepath.Join(in.workDir, ".claude", "settings.local.json")
	projectSettings := filepath.Join(in.workDir, ".claude", "settings.json")
	for _, path := range []string{projectLocal, projectSettings} {
		if member, ok := settingsMember(path, "statusLine"); ok && member.Found {
			return check{name: name, detail: path + " overrides statusLine"}
		}
	}
	for _, path := range []string{projectLocal, projectSettings, in.settingsPath} {
		member, ok := settingsMember(path, "disableAllHooks")
		if !ok {
			continue
		}
		var disabled bool
		if json.Unmarshal(member.Value, &disabled) == nil && disabled {
			return check{name: name, detail: path + " sets disableAllHooks"}
		}
	}
	return check{name: name, ok: true, detail: "none in " + in.workDir}
}

// settingsMember reads one top-level member of a settings file. A missing or
// unreadable file reports false: doctor never invents an override.
func settingsMember(path, key string) (settings.Member, bool) {
	file, existed, err := settings.Load(path)
	if err != nil || !existed {
		return settings.Member{}, false
	}
	member, err := settings.Get(file, key)
	if err != nil {
		return settings.Member{}, false
	}
	return member, true
}

func checkRenderer(ctx context.Context, cfg config.Config, in doctorInput) check {
	if cfg.Renderer.Command == "" {
		return check{name: "renderer", ok: true, detail: "none configured"}
	}
	if err := in.runRenderer(ctx, cfg.Renderer.Command); err != nil {
		return check{name: "renderer", detail: cfg.Renderer.Command + ": " + err.Error()}
	}
	return check{name: "renderer", ok: true, detail: cfg.Renderer.Command + " ran"}
}

func checkIdentity(cfg config.Config) check {
	_, nodeErr := uuid.Parse(cfg.Node.ID)
	_, accountErr := uuid.Parse(cfg.Account.ID)
	ok := nodeErr == nil && accountErr == nil && cfg.Node.Alias != "" && cfg.Account.Alias != ""
	return check{name: "identity", ok: ok, detail: cfg.Node.Alias + " / " + cfg.Account.Alias}
}

func checkHomeDirectory(home string) check {
	name := "home directory"
	if _, err := os.Stat(home); err != nil {
		return check{name: name, detail: err.Error()}
	}
	if _, err := store.LoadState(home); err != nil {
		return check{name: name, detail: "state: " + err.Error()}
	}
	probe, err := os.CreateTemp(filepath.Join(home, store.PendingDir), ".doctor-*")
	if err != nil {
		return check{name: name, detail: "spool not writable: " + err.Error()}
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return check{name: name, ok: true, detail: home}
}

func checkQuotaWindows(state store.State, now time.Time) check {
	detail := fmt.Sprintf("5h %s, 7d %s, last observation %s", state.Windows.FiveHour.Status, state.Windows.SevenDay.Status, ago(state.LastObservedAt, now))
	return check{name: "quota windows", ok: true, detail: detail}
}

func checkRefreshInterval(in doctorInput) check {
	name := "refreshInterval"
	notSet := check{name: name, ok: true, detail: "not set"}
	member, ok := settingsMember(in.settingsPath, "statusLine")
	if !ok || !member.Found {
		return notSet
	}
	interval, err := settings.Get(member.Value, "refreshInterval")
	if err != nil || !interval.Found {
		return notSet
	}
	var seconds json.Number
	if json.Unmarshal(interval.Value, &seconds) != nil {
		return notSet
	}
	return check{name: name, ok: true, detail: seconds.String() + "s; each tick runs gaugewire"}
}

func checkSpool(home string) check {
	pending, dead, err := store.Counts(home)
	if err != nil {
		return check{name: "spool", detail: err.Error()}
	}
	entries, unreadable, err := store.ListDeadLetters(home)
	if err != nil {
		return check{name: "spool", detail: err.Error()}
	}
	detail := strconv.Itoa(pending) + " pending, " + strconv.Itoa(dead) + " dead-lettered"
	if len(entries) > 0 {
		detail += ", newest: " + entries[0].Reason
	}
	if unreadable > 0 {
		detail += ", " + strconv.Itoa(unreadable) + " unreadable"
	}
	return check{name: "spool", ok: true, detail: detail}
}
