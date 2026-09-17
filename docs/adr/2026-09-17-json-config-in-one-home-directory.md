---
tags: config, storage, platform
status: accepted
decision-date: 2026-09-17
---

# JSON configuration and all state in one home directory

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

The original product spec proposed `config.yaml` and separate configuration and data
directories. Gaugewire has a small configuration, must not depend on a database, and should be
easy to inspect, back up and remove on three operating systems.

## Options considered

1. YAML configuration with a YAML dependency, configuration and data in separate
   platform-specific directories.
2. JSON configuration through the standard library, everything under one directory.

## Decision

Option 2. `config.json`, `state.json`, the lock files, `pending/`, `dead-letter/`, `logs/` and an
optional `databox.key` live under `os.UserConfigDir()/gaugewire`, overridable by
`GAUGEWIRE_HOME`. Directories are `0700` and files `0600` on Unix. Every write goes through a
temporary file, `fsync` and an atomic rename.

## Consequences

- Zero configuration dependencies; the same JSON tooling reads config, state and events.
- No comments in the configuration file. Field documentation lives in the spec and in
  `gaugewire doctor` output.
- One directory to back up, inspect or delete with `gaugewire uninstall --purge`.
