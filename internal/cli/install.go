package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/settings"
	"github.com/sulcer/gaugewire/internal/store"
)

// ErrAlreadyInstalled means the status line already runs gaugewire.
var ErrAlreadyInstalled = errors.New("status line already points at gaugewire; pass --force to reinstall")

// ErrNoInstallRecord means the status line runs gaugewire but config.json has
// no record of what it replaced, so reinstalling would lose the original.
var ErrNoInstallRecord = errors.New("status line already points at gaugewire but config.json has no install record; restore settings.json from the newest .gaugewire-backup-* file and run install again")

const defaultAccountAlias = "claude-01"

type installOptions struct {
	settingsPath string
	nodeAlias    string
	accountAlias string
	force        bool
	executable   string
	now          func() time.Time
	hostname     func() (string, error)
}

// runInstall parses the flags and installs with the real executable, clock and hostname.
func runInstall(_ context.Context, args []string, _ BuildInfo, streams IO) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(streams.Stderr)
	opts := installOptions{now: time.Now, hostname: os.Hostname}
	flags.StringVar(&opts.settingsPath, "settings", "", "Claude Code settings file (default: the user settings file)")
	flags.StringVar(&opts.nodeAlias, "node-alias", "", "node alias (default: hostname)")
	flags.StringVar(&opts.accountAlias, "account-alias", "", "account alias (default: claude-01)")
	flags.BoolVar(&opts.force, "force", false, "reinstall over an existing gaugewire status line")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if opts.settingsPath == "" {
		path, err := settings.DefaultPath()
		if err != nil {
			return err
		}
		opts.settingsPath = path
	}
	absolute, err := filepath.Abs(opts.settingsPath)
	if err != nil {
		return fmt.Errorf("resolve settings path: %w", err)
	}
	opts.settingsPath = absolute
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	opts.executable = exe
	home, err := store.Home()
	if err != nil {
		return err
	}
	return install(home, opts, streams.Stdout)
}

// install loads or creates config.json, splices the status-line command into
// the settings file and records what it changed. config.json is saved before
// the settings file is touched, so every intermediate state still knows how to
// get back to the user's original status line.
func install(home string, opts installOptions, stdout io.Writer) error {
	cfg, err := loadOrCreateConfig(home, opts)
	if err != nil {
		return err
	}
	file, existed, err := settings.Load(opts.settingsPath)
	if err != nil {
		return err
	}
	current, err := settings.Get(file, "statusLine")
	if err != nil {
		return err
	}
	command := installedCommand(opts.executable)
	currentCommand := commandOf(current.Value)
	alreadyOurs := currentCommand != "" &&
		(currentCommand == command || (cfg.Install != nil && currentCommand == cfg.Install.InstalledCommand) || isGaugewire(currentCommand))
	switch {
	case alreadyOurs && cfg.Install == nil:
		return ErrNoInstallRecord
	case alreadyOurs && !opts.force:
		return ErrAlreadyInstalled
	}
	newValue, err := statusLineWith(current.Value, command)
	if err != nil {
		return err
	}
	updated, err := settings.Set(file, "statusLine", newValue)
	if err != nil {
		return err
	}
	if !alreadyOurs {
		cfg.Renderer.Command = currentCommand
		cfg.Install = &config.Install{OriginalStatusLine: current.Value}
	}
	cfg.Install.SettingsPath = opts.settingsPath
	cfg.Install.InstalledCommand = command
	if err := store.EnsureLayout(home); err != nil {
		return err
	}
	if err := config.Save(home, cfg); err != nil {
		return err
	}
	backup := "none (file did not exist)"
	mode := os.FileMode(0o600)
	if existed {
		if info, statErr := os.Stat(opts.settingsPath); statErr == nil {
			mode = info.Mode().Perm()
		}
		backup = backupPath(opts.settingsPath, opts.now())
		if err := store.WriteFileAtomic(backup, file, 0o600); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
	} else if err := os.MkdirAll(filepath.Dir(opts.settingsPath), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	if err := store.WriteFileAtomic(opts.settingsPath, updated, mode); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	renderer := cfg.Renderer.Command
	if renderer == "" {
		renderer = "none"
	}
	fmt.Fprintf(stdout, "settings:     %s\nbackup:       %s\nstatus line:  %s\nrenderer:     %s\nnode:         %s %s\naccount:      %s %s\n",
		opts.settingsPath, backup, command, renderer, cfg.Node.Alias, cfg.Node.ID, cfg.Account.Alias, cfg.Account.ID)
	return nil
}

func loadOrCreateConfig(home string, opts installOptions) (config.Config, error) {
	cfg, err := config.Load(home)
	switch {
	case errors.Is(err, config.ErrMissing):
		cfg = config.Default()
	case err != nil:
		return config.Config{}, err
	}
	if cfg.Node.ID == "" {
		cfg.Node.ID = uuid.NewV4().String()
	}
	if cfg.Account.ID == "" {
		cfg.Account.ID = uuid.NewV4().String()
	}
	if opts.nodeAlias != "" {
		cfg.Node.Alias = opts.nodeAlias
	}
	if cfg.Node.Alias == "" {
		host, err := opts.hostname()
		if err != nil || host == "" {
			host = "node-01"
		}
		cfg.Node.Alias = host
	}
	if opts.accountAlias != "" {
		cfg.Account.Alias = opts.accountAlias
	}
	if cfg.Account.Alias == "" {
		cfg.Account.Alias = defaultAccountAlias
	}
	return cfg, nil
}

// installedCommand builds the status-line command for this binary. Paths use
// forward slashes, written with strings.ReplaceAll rather than filepath.ToSlash
// so the conversion happens on every platform, because Git Bash strips
// unquoted backslashes; the path is quoted only when it contains whitespace.
func installedCommand(executable string) string {
	path := strings.ReplaceAll(executable, `\`, "/")
	if strings.ContainsAny(path, " \t") {
		path = `"` + path + `"`
	}
	return path + " statusline"
}

// backupPath is the timestamped backup name, suffixed -2, -3, … so a second
// install in the same second never overwrites the first backup.
func backupPath(settingsPath string, at time.Time) string {
	base := settingsPath + ".gaugewire-backup-" + at.UTC().Format("20060102-150405")
	path := base
	for n := 2; ; n++ {
		if _, err := os.Stat(path); err != nil {
			return path
		}
		path = base + "-" + strconv.Itoa(n)
	}
}

func isGaugewire(command string) bool {
	trimmed := strings.TrimSpace(command)
	return strings.HasSuffix(trimmed, "gaugewire statusline") || strings.HasSuffix(trimmed, "gaugewire.exe statusline") ||
		strings.HasSuffix(trimmed, `gaugewire" statusline`) || strings.HasSuffix(trimmed, `gaugewire.exe" statusline`)
}

// commandOf returns the command string of a statusLine object, or "" when
// there is no object or no command.
func commandOf(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}
	member, err := settings.Get(value, "command")
	if err != nil || !member.Found {
		return ""
	}
	var command string
	if json.Unmarshal(member.Value, &command) != nil {
		return ""
	}
	return command
}

// statusLineWith returns the statusLine object with its command replaced and a
// "type" added when it had none. An absent value, or an object with no members,
// is replaced by a fresh {"type":"command","command":...} object: neither holds
// a setting worth keeping, and both would otherwise leave a typeless statusLine.
func statusLineWith(value json.RawMessage, command string) (json.RawMessage, error) {
	quoted, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	fresh := json.RawMessage(`{"type":"command","command":` + string(quoted) + `}`)
	if len(value) == 0 {
		return fresh, nil
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(value, &members) != nil || members == nil {
		return nil, fmt.Errorf("statusLine is not an object: %w", settings.ErrNotObject)
	}
	if len(members) == 0 {
		return fresh, nil
	}
	updated, err := settings.Set(value, "command", quoted)
	if err != nil {
		return nil, fmt.Errorf("statusLine is not an object: %w", err)
	}
	if _, ok := members["type"]; ok {
		return updated, nil
	}
	return settings.Set(updated, "type", json.RawMessage(`"command"`))
}
