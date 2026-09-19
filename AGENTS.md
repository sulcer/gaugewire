# AGENTS.md

Rulebook entry point for Gaugewire, for humans and AI agents alike. The detailed rules live in
[`.claude/rules/`](.claude/rules/); the reasoning behind them lives in
[`docs/why-these-rules.md`](docs/why-these-rules.md). `CLAUDE.md` imports this file.

## Goal of these rules

Any contributor, human or agent, produces the same code structure, naming, tests and docs for
the same task. When a choice is not covered: copy the nearest existing example in this repo,
or propose an ADR, or ask. Never improvise silently.

## Trust hierarchy

Read in this order, trust in this order:

1. This file.
2. [`.claude/rules/`](.claude/rules/). Always-on rules load every session; path-scoped rules
   load when you touch matching files.
3. [`docs/adr/`](docs/adr/README.md). Accepted decisions. Never deleted, only superseded or amended.
4. [`docs/spec/`](docs/spec/README.md). The current design, stated as fact.
5. [`docs/how-tos/`](docs/how-tos/). Procedures. Useful, not authoritative.

[`docs/research/`](docs/research/) is frozen. Never cite it from code, an ADR or a spec except
through a spec page's provenance footer.

## What this is

Gaugewire observes Claude Code subscription quota on each machine of a fleet, reading the
documented status-line JSON, keeps one durable machine state, and publishes normalized events
to sinks. Databox is the first sink. Start at
[`docs/spec/gaugewire/README.md`](docs/spec/gaugewire/README.md).

## Stack

| Concern | Choice |
|---|---|
| Language | Go 1.27, `toolchain go1.27.1`, `CGO_ENABLED=0` for releases |
| Dependencies | Standard library. Allowed exceptions: `github.com/gofrs/flock`, and `github.com/google/go-cmp` in tests. Anything else needs an ADR. |
| JSON | `encoding/json` v1 API |
| Logging | `log/slog` |
| Dev tools | `tools/go.mod` tool directives: gofumpt, govulncheck, golangci-lint v2, goreleaser v2 |
| Release | goreleaser on `v*` tags: darwin and linux on amd64 and arm64, windows on amd64 |

## Repository map

```
cmd/gaugewire/           entry point
internal/cli/            subcommands
internal/quota/          domain: snapshot, windows, reducer, dedupe (pure, no I/O)
internal/source/claude/  status-line JSON to observation
internal/store/          home directory, atomic writes, locks, state, spool
internal/settings/       edit one member of Claude Code's settings file by byte offsets
internal/renderer/       the user's previous status-line command
internal/sink/           Sink and Flusher; internal/sink/databox/ is the first sink
internal/config/         config.json
fixtures/statusline/     real and synthetic stdin payloads
tools/                   pinned dev tools
githooks/                pre-commit, commit-msg
scripts/                 coverage gate
docs/                    adr, spec, plans, how-tos, research
```

Dependency direction: `source → quota`, `quota → nothing`, `settings → nothing`,
`store → quota`, `sink → store, quota`, `sink/databox → sink, quota`, `cli → everything`. Nothing imports `cli`.

## Commands

| Command | Runs |
|---|---|
| `make setup` | `git config core.hooksPath githooks` and warms the pinned tools |
| `make fmt` | `golangci-lint fmt`: gofumpt and import grouping |
| `make check` | format check, `go vet ./...`, `golangci-lint config verify && run`, `go test -race -shuffle=on -count=1 ./...` |
| `make test-integration` | `go test -race -tags integration -count=1 ./...` |
| `make cover` | coverage profile and `scripts/coverage-gate.sh` |
| `make vuln` | `govulncheck ./...` |
| `make snapshot` | `goreleaser release --snapshot --clean` |
| `make ci` | everything CI runs, except the macOS job |

Every target wraps exactly the command CI runs. There is no CI-only gate.

## Conventions in one screen

- Branch `<type>/<topic>`. Never push to `main`. Pull requests use the template and carry
  `--assignee sulcer --label patch`; their description never carries a generated-by footer.
- Commits: Conventional Commits **without a scope**, lowercase subject, at most 72 characters,
  ending with the `Co-Authored-By` trailer for the running model. The commit-msg hook enforces
  the subject. Never bypass a hook with `--no-verify`; fix the cause and commit again.
- Errors wrap with `%w`; sentinels live in the owning package; `os.Exit` only in `main`.
- Tests: table-driven, `t.Parallel()`, whole-value assertions, one assertion per test, and
  expected values that come from the spec or a fixture, never from running the code under test.
- A behaviour-changing change gets an ADR. A spec page that now describes built behaviour flips
  its marker in the same pull request. A plan is deleted when its feature lands.
- Third-party behaviour is relied on only when its public documentation states it. Anything
  learned another way is an assumption to verify, never a fact.
- Never: read `.env*` or key files, modify `~/.claude/settings.json` outside the `install` and
  `uninstall` commands, run destructive git without approval, or skip the gate.

## Rules index

| File | Scope |
|---|---|
| [`always-on/workflow.md`](.claude/rules/always-on/workflow.md) | every session |
| [`always-on/adr-and-spec-discipline.md`](.claude/rules/always-on/adr-and-spec-discipline.md) | every session |
| [`always-on/simplicity.md`](.claude/rules/always-on/simplicity.md) | every session |
| [`code/go-code.md`](.claude/rules/code/go-code.md) | `cmd/**/*.go`, `internal/**/*.go` |
| [`code/go-testing.md`](.claude/rules/code/go-testing.md) | `**/*_test.go`, `testdata/**`, `fixtures/**` |
| [`code/dependencies.md`](.claude/rules/code/dependencies.md) | `go.mod`, `go.sum`, `tools/**` |
| [`code/ci-and-release.md`](.claude/rules/code/ci-and-release.md) | `.github/**`, `.goreleaser.yaml`, `Makefile`, `githooks/**`, `scripts/**` |

## For agents

Read first: this file, then the spec page for the area you touch, then any ADR it links. The
hooks in `.claude/settings.json` run gofumpt on every Go file you edit or write (the path is
read from the hook's stdin) and run build and tests when a turn ends. A subagent does not
inherit these rules: hand it the rule files whose paths match what it will touch, plus the
spec page.
