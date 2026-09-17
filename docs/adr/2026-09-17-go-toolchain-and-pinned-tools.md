---
tags: toolchain, dependencies, build
status: accepted
decision-date: 2026-09-17
---

# Go 1.27, pinned tools in a tools module, standard library first

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

The project is a single cross-platform Go binary meant to be production quality and current.
The choices that shape every later change are the Go version, how developer tools are pinned,
which dependencies are allowed, and which JSON API is used.

## Options considered

- **Go version:** 1.27 (current stable) or 1.26 (previous stable, installed on the owner's
  machine). golangci-lint gained 1.27 support in v2.13.0 and staticcheck in 2026.2, so both are
  viable.
- **Tool pinning:** `go install ...@latest` in a Makefile; a tool-version manager; `tool`
  directives in the main `go.mod`; `tool` directives in a separate `tools/go.mod`.
- **Dependencies:** a CLI framework, a config library, a logging library and a UUID library, or
  the standard library.
- **JSON:** `encoding/json` v1 API or `encoding/json/v2`.

## Decision

1. `go 1.27` with `toolchain go1.27.1`. Machines on 1.26 download it automatically.
2. Dev tools live in `tools/go.mod` as `tool` directives and run through
   `go tool -modfile=tools/go.mod`. Versions are in git, the product module graph stays clean,
   and local and CI runs use identical binaries.
3. Standard library everywhere it covers the need: `flag`, `encoding/json`, `net/http`,
   `log/slog`, `uuid`, `testing`. Two exceptions: `github.com/gofrs/flock` for cross-platform
   file locks and `github.com/google/go-cmp` for whole-struct diffs in tests.
4. The `encoding/json` v1 API, because Gaugewire reads and rewrites a user-authored
   `settings.json` and the v2 defaults (case-sensitive names, duplicate keys rejected) would
   surprise users. Go 1.27 runs the v1 API on the v2 engine.

## Consequences

- Building golangci-lint from source takes minutes on a cold cache; the build cache makes later
  runs fast. CI caches on both `go.sum` files.
- A new dependency needs a one-line ADR and a `govulncheck` result.
- Direct use of `encoding/json/v2` is deferred until a concrete need appears.
