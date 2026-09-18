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

	"github.com/sulcer/gaugewire/internal/renderer"
	"github.com/sulcer/gaugewire/internal/settings"
	"github.com/sulcer/gaugewire/internal/store"
)

// ErrUnhealthy makes doctor exit 1 when any check fails.
var ErrUnhealthy = errors.New("one or more checks failed")

// samplePayload is what doctor feeds the renderer: the smallest payload the
// hot path itself accepts.
const samplePayload = `{"version":"2.1.274","rate_limits":{"five_hour":{"used_percentage":24,"resets_at":1789669200},"seven_day":{"used_percentage":53.5,"resets_at":1789722000}}}`

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
	if *settingsPath == "" {
		path, err := settings.DefaultPath()
		if err != nil {
			return err
		}
		*settingsPath = path
	}
	home, err := store.Home()
	if err != nil {
		return err
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	in := doctorInput{home: home, settingsPath: *settingsPath, workDir: workDir, now: time.Now(), runRenderer: runRendererSample}
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

func runRendererSample(ctx context.Context, command string) error {
	ctx, cancel := context.WithTimeout(ctx, rendererTimeout)
	defer cancel()
	return renderer.Run(ctx, command, []byte(samplePayload), io.Discard, io.Discard)
}

// diagnose runs every offline check in display order.
func diagnose(ctx context.Context, in doctorInput) []check {
	cfg, cfgErr := loadConfigForDoctor(in.home)
	state, _ := store.LoadState(in.home)
	return []check{
		checkConfiguration(cfgErr),
		checkClaudeCodeVersion(state),
		checkStatusLineIntegration(cfg, in),
		checkOverrides(in),
		checkRenderer(ctx, cfg, in),
		checkIdentity(cfg),
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
