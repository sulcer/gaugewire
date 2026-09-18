package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/renderer"
	"github.com/sulcer/gaugewire/internal/settings"
	"github.com/sulcer/gaugewire/internal/store"
)

// ErrUnhealthy makes doctor exit 1 when any check fails.
var ErrUnhealthy = errors.New("one or more checks failed")

// samplePayload is what doctor feeds the renderer. It carries every documented
// status-line field a renderer is likely to read, so a renderer that indexes
// into the payload does not fail on the sample alone.
const samplePayload = `{"session_id":"doctor","cwd":"/","model":{"id":"claude-sonnet-5","display_name":"Sonnet 5"},"workspace":{"current_dir":"/","project_dir":"/"},"version":"2.1.274","rate_limits":{"five_hour":{"used_percentage":24,"resets_at":1789669200},"seven_day":{"used_percentage":53.5,"resets_at":1789722000}}}`

const rendererTimeout = 5 * time.Second

type check struct {
	name   string
	ok     bool
	detail string
}

type doctorInput struct {
	home         string
	settingsPath string
	workDir      string
	now          time.Time
	runRenderer  func(ctx context.Context, command string) error

	// cfg is the decoded config.json and cfgErr whatever loading it returned.
	// Load returns the decoded value alongside ErrInvalid, so every row after
	// the configuration row still describes a config.json that failed validation.
	cfg    config.Config
	cfgErr error
}

func runDoctor(ctx context.Context, args []string, _ BuildInfo, streams IO) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(streams.Stderr)
	settingsPath := flags.String("settings", "", "Claude Code settings file (default: the user settings file)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	cfg, cfgErr := config.Load(home)
	if *settingsPath == "" {
		if *settingsPath, err = recordedOrDefaultSettingsPath(cfg); err != nil {
			return err
		}
	}
	if *settingsPath, err = resolveSettingsPath(*settingsPath); err != nil {
		return err
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	in := doctorInput{home: home, settingsPath: *settingsPath, workDir: workDir, now: time.Now(), runRenderer: runRendererSample, cfg: cfg, cfgErr: cfgErr}
	checks := diagnose(ctx, in)
	if _, err := io.WriteString(streams.Stdout, renderDoctor(checks)); err != nil {
		return err
	}
	for _, c := range checks {
		if !c.ok {
			return ErrUnhealthy
		}
	}
	return nil
}

// recordedOrDefaultSettingsPath prefers the file install edited, so doctor
// checks the same file even when it is not the user settings file. An invalid
// config.json still carries the record: Load returns the decoded value with it.
func recordedOrDefaultSettingsPath(cfg config.Config) (string, error) {
	if cfg.Install != nil && cfg.Install.SettingsPath != "" {
		return cfg.Install.SettingsPath, nil
	}
	return settings.DefaultPath()
}

func runRendererSample(ctx context.Context, command string) error {
	ctx, cancel := context.WithTimeout(ctx, rendererTimeout)
	defer cancel()
	return renderer.Run(ctx, command, []byte(samplePayload), io.Discard, io.Discard)
}

// diagnose runs every offline check in display order.
func diagnose(ctx context.Context, in doctorInput) []check {
	state, _ := store.LoadState(in.home)
	return []check{
		checkConfiguration(in.cfgErr),
		checkClaudeCodeVersion(state),
		checkStatusLineIntegration(in.cfg, in),
		checkOverrides(in),
		checkRenderer(ctx, in.cfg, in),
		checkIdentity(in.cfg),
		checkHomeDirectory(in.home),
		checkQuotaWindows(state, in.now),
		checkRefreshInterval(in),
		checkSpool(in.home),
	}
}

func renderDoctor(checks []check) string {
	var b strings.Builder
	healthy := true
	for _, c := range checks {
		mark := "✓"
		if !c.ok {
			mark = "✗"
			healthy = false
		}
		fmt.Fprintf(&b, "%s %s: %s\n", mark, c.name, c.detail)
	}
	b.WriteString("\n")
	if healthy {
		b.WriteString("HEALTHY\n")
	} else {
		b.WriteString("UNHEALTHY\n")
	}
	return b.String()
}
