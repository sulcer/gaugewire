# How to install Gaugewire on a machine

Practical procedure, not authoritative. See [`cli-and-install`](../spec/gaugewire/cli-and-install.md)
for the algorithm and [`architecture`](../spec/gaugewire/architecture.md) for the home directory.

## Prerequisites

- Claude Code 2.1.251 or newer, logged into the subscription this machine should report.
- The `gaugewire` binary for this OS and architecture, from a release build.

## 1. Install

```bash
gaugewire install --node-alias <name>
```

`--node-alias` is this machine's label in the fleet (default: the hostname); pass
`--account-alias` too when more than one Claude account is in play (default: `claude-01`).
Install reads `~/.claude/settings.json` (or the file `--settings <path>` names), saves whatever
`statusLine` command was already configured so it keeps running, and points the setting at
`gaugewire statusline` instead. Nothing else in the file changes.

If the settings file already existed, install writes a timestamped backup next to it:
`settings.json.gaugewire-backup-<UTC timestamp>`, a byte-exact copy of the file as it stood
before install touched it. Keep this file; it is the only exact copy of the pre-install state.

Running `install` again once Gaugewire is already the status line does nothing and refuses,
unless `--force` is passed to reinstall over it (this keeps the originally saved command and
only refreshes the path to the binary).

## 2. Verify

```bash
gaugewire doctor
```

Every check should print `✓`; the command exits 0 when it does. `✗` lines name what to fix, most
often a project `.claude/settings.local.json` or `.claude/settings.json` overriding the status
line, or `disableAllHooks: true` in one of the settings files. Start Claude Code, or restart a
running session, so it picks up the new status line.

`gaugewire status` shows the current quota windows and spool state once at least one
observation has happened.

## 3. Uninstall

```bash
gaugewire uninstall
```

Restores the exact `statusLine` object install saved, as long as the setting still points at
Gaugewire; otherwise it warns and leaves the file alone. `--settings <path>` overrides the
settings file if it moved since install. `--purge` also deletes Gaugewire's home directory
(state, spool, `config.json`); without it, local state and any spooled events stay on disk.

## References

- [Claude Code settings reference](https://code.claude.com/docs/en/settings-reference) —
  `statusLine` object shape.
- [Claude Code status line](https://code.claude.com/docs/en/statusline) — where the settings
  file lives and how the command runs.
- [Claude Code settings](https://code.claude.com/docs/en/settings) — file precedence, relevant to
  `doctor`'s overrides check.
