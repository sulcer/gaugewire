---
tags: quality, ci, hooks, agents
status: accepted
decision-date: 2026-09-17
---

# Mechanical gates run as hooks, with one command shared by hook and CI

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

Formatting, vetting, linting and testing are mechanical. Written as prose rules they are
followed most of the time; the repo should make them run every time, for humans and for agents,
without a CI-only gate that arrives minutes later.

## Options considered

- **Git hooks:** a hook manager binary on every machine, or two POSIX scripts activated by
  `git config core.hooksPath`.
- **Agent gates:** prose in the rulebook, or Claude Code hooks that the harness runs.
- **CI matrix:** all three operating systems on every PR (macOS minutes cost ten times Linux
  minutes on a private repo), Ubuntu only, or Ubuntu and Windows per PR with macOS on `main`
  and tags.

## Decision

1. Two plain scripts under `githooks/`: `pre-commit` runs the formatter check, `go vet` and
   golangci-lint; `commit-msg` enforces Conventional Commits without a scope. `make setup`
   points `core.hooksPath` at them. `--no-verify` is never used.
2. Claude Code hooks in `.claude/settings.json`: gofumpt on every edited or written Go file,
   and build plus tests when a turn ends, reporting failures as context.
3. CI runs the same commands: lint, tests with the race detector on Ubuntu and Windows for every
   PR, macOS on pushes to `main` and on tags, coverage gated on the pure packages, govulncheck
   on every PR and weekly, a goreleaser snapshot to prove cross-compilation.
4. The Makefile only wraps commands CI also runs, because Windows runners may lack `make`.

## Consequences

- Windows gates every PR because that is where detached processes, file locks and the Git Bash
  versus PowerShell shell choice will break.
- Cross-platform bugs specific to macOS surface at merge time rather than PR time. Acceptable
  because development happens on macOS.
- Rule files stay short: they hold judgment calls, not checklists.
