# Repository Scaffold Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the empty repository into a gated, releasable Go project with a walking-skeleton binary, so every later feature lands on identical tooling, hooks, CI, rulebook and docs.

**Architecture:** One Go module (`github.com/sulcer/gaugewire`) plus a nested `tools/` module that pins the dev tools through `tool` directives. Every mechanical check has exactly one command; the Makefile wraps it, a git hook runs it, a Claude Code hook runs it, and CI runs it. Documentation follows the layout already in `docs/`.

**Tech Stack:** Go 1.27.1, standard library only in this plan; gofumpt v0.12.0, govulncheck v1.8.0, golangci-lint v2.13.2, goreleaser v2.18.2; GitHub Actions; POSIX `sh` hooks.

**Spec:** `docs/spec/repo-scaffold/README.md`, with `docs/adr/2026-09-17-go-toolchain-and-pinned-tools.md`, `docs/adr/2026-09-17-gates-as-hooks.md` and `docs/adr/2026-09-17-documentation-system.md`.

## Global Constraints

- Module path `github.com/sulcer/gaugewire`; `go 1.27`; `toolchain go1.27.1`. A machine on Go 1.26 downloads 1.27.1 automatically (`GOTOOLCHAIN=auto` is the default).
- No runtime or test dependency is added in this plan. Later plans add `github.com/gofrs/flock` and `github.com/google/go-cmp` only.
- Tool versions: gofumpt `v0.12.0`, govulncheck `v1.8.0`, golangci-lint `v2.13.2`, goreleaser `v2.18.2`. They are invoked only as `go tool -modfile=tools/go.mod <name>`.
- Every check has one command. The Makefile only wraps commands CI also runs. CI calls `go` directly, because Windows runners may lack `make`.
- Branch `chore/repo-scaffold`. Commits are Conventional Commits **without a scope**, lowercase subject, at most 72 characters, and end with the trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` written through a HEREDOC.
- **Commit authority:** the owner approves every commit and every push. At each commit step, propose the message and wait, unless autonomous mode was granted for that session. Pushing always needs explicit approval.
- `gh pr create` must carry `--assignee sulcer --label patch`. A PR description never carries a "Generated with Claude Code" footer.
- Third-party behaviour is relied on only when its public documentation states it; anything else is recorded as an assumption to verify.
- Scripts under `githooks/`, `scripts/` and `.claude/hooks/` are POSIX `sh` with no `jq` and no `python`, so they run under Git for Windows.
- GitHub Actions are pinned by commit SHA: `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1` (v7.0.1), `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` (v7.0.0).
- **Create files with the Write tool, not a shell heredoc.** Several files below contain the text of git's hook-bypass flag; the machine's git-ops hook blocks any Bash command containing it, even when no git command is involved.

## Before you start

1. `docs/` and `.gitignore` are untracked on an unborn `main`. With the owner's approval, make the first commit on `main` and push it, so later work has a base branch:

   ```bash
   git add .gitignore docs
   git commit -m "$(cat <<'EOF'
   docs: add product spec, decisions and scaffold spec

   Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
   EOF
   )"
   git push -u origin main
   ```

2. Then create the working branch: `git switch -c chore/repo-scaffold`.

---

## File structure

| Path | Responsibility |
|---|---|
| `go.mod` | Product module: path, Go version, toolchain |
| `tools/go.mod`, `tools/go.sum` | Pinned dev tools through `tool` directives |
| `Makefile` | Thin wrappers around the exact commands CI runs |
| `.editorconfig`, `.gitattributes`, `.gitignore` | Editor and git hygiene |
| `cmd/gaugewire/main.go` | Entry point: build info, call `cli.Run`, exit code |
| `cmd/gaugewire/main_integration_test.go` | Builds the binary and runs it (build tag `integration`) |
| `internal/cli/cli.go` | `BuildInfo`, `ErrUsage`, `Run` dispatch |
| `internal/cli/version.go` | `version` command and build-info fallback |
| `internal/cli/cli_test.go`, `internal/cli/version_test.go` | Unit tests |
| `.golangci.yml` | Lint and formatter configuration, v2 schema |
| `githooks/pre-commit`, `githooks/commit-msg` | Git gates |
| `scripts/coverage-gate.sh` | Hard coverage gate on the pure packages |
| `.claude/settings.json`, `.claude/hooks/verify.sh` | Agent permissions and hooks |
| `.claude/rules/always-on/*.md`, `.claude/rules/code/*.md` | Rulebook |
| `AGENTS.md`, `CLAUDE.md`, `README.md`, `CONTRIBUTING.md` | Entry documents |
| `.github/workflows/ci.yml`, `.github/workflows/release.yml` | CI and release |
| `.github/dependabot.yml`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/CODEOWNERS` | Repository automation |
| `.goreleaser.yaml` | Cross-platform release builds |

---

### Task 1: Modules, Makefile and hygiene files

**Files:**
- Create: `go.mod`, `tools/go.mod`, `tools/go.sum`, `Makefile`, `.editorconfig`, `.gitattributes`
- Modify: `.gitignore`

**Interfaces:**
- Produces: the invocation `go tool -modfile=tools/go.mod <gofumpt|govulncheck|golangci-lint|goreleaser>`, and the Makefile targets `setup tools fmt fmt-check vet lint tidy-check test test-integration cover vuln build snapshot check ci clean` that every later task and every CI job uses.

- [ ] **Step 1: Write `go.mod`**

```
module github.com/sulcer/gaugewire

go 1.27

toolchain go1.27.1
```

- [ ] **Step 2: Write `tools/go.mod` by hand**

Writing it rather than `go mod init` avoids the conservative version the tool would pick.

```
module github.com/sulcer/gaugewire/tools

go 1.27

toolchain go1.27.1
```

- [ ] **Step 3: Add the pinned tools**

```bash
cd tools
go get -tool mvdan.cc/gofumpt@v0.12.0
go get -tool golang.org/x/vuln/cmd/govulncheck@v1.8.0
go get -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
go get -tool github.com/goreleaser/goreleaser/v2@v2.18.2
go mod tidy
cd ..
```

Expected: `tools/go.mod` gains a `tool (...)` block with the four paths, and `tools/go.sum` appears. The first command downloads Go 1.27.1.

- [ ] **Step 4: Verify the tools run from the repository root**

```bash
go tool -modfile=tools/go.mod gofumpt -version
go tool -modfile=tools/go.mod golangci-lint version
```

Expected: `v0.12.0`, and a line containing `2.13.2`. Building golangci-lint takes a few minutes on a cold cache; later runs are instant.

- [ ] **Step 5: Write `Makefile`**

```make
.DEFAULT_GOAL := help
SHELL := /bin/sh
TOOL := go tool -modfile=tools/go.mod
PKGS := ./...

.PHONY: help setup tools fmt fmt-check vet lint tidy-check test test-integration cover vuln build snapshot check ci clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

setup: tools ## One-time machine setup: git hooks and tool warm-up
	git config core.hooksPath githooks

tools: ## Build the pinned dev tools into the Go build cache
	$(TOOL) gofumpt -version
	$(TOOL) govulncheck -version
	$(TOOL) golangci-lint version
	$(TOOL) goreleaser --version

fmt: ## Format Go files in place (gofumpt and import grouping)
	$(TOOL) golangci-lint fmt

fmt-check: ## Fail if any Go file is not gofumpt-formatted
	@out="$$($(TOOL) gofumpt -l .)"; if [ -n "$$out" ]; then echo "$$out"; echo "run: make fmt"; exit 1; fi

vet: ## go vet
	go vet $(PKGS)

lint: ## golangci-lint, config verified first
	$(TOOL) golangci-lint config verify
	$(TOOL) golangci-lint run

tidy-check: ## Fail if either module is untidy
	go mod tidy -diff
	cd tools && go mod tidy -diff

test: ## Unit tests with the race detector and shuffling
	go test -race -shuffle=on -count=1 $(PKGS)

test-integration: ## Integration tests (build tag)
	go test -race -tags integration -count=1 $(PKGS)

cover: ## Coverage profile and the hard gate on the pure packages
	go test -coverprofile=coverage.out -covermode=atomic $(PKGS)
	go tool cover -func=coverage.out | tail -1
	sh scripts/coverage-gate.sh

vuln: ## govulncheck
	$(TOOL) govulncheck $(PKGS)

build: ## Build the binary into ./gaugewire
	go build -trimpath -o gaugewire ./cmd/gaugewire

snapshot: ## goreleaser snapshot build for every platform into dist/
	$(TOOL) goreleaser release --snapshot --clean

check: fmt-check vet lint test ## The pre-commit gate plus unit tests

ci: tidy-check check test-integration cover vuln ## Everything CI runs, except the macOS job

clean: ## Remove build outputs
	rm -rf gaugewire gaugewire.exe dist coverage.out
```

- [ ] **Step 6: Write `.editorconfig`**

```ini
root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
trim_trailing_whitespace = true
indent_style = space
indent_size = 2

[*.go]
indent_style = tab

[Makefile]
indent_style = tab

[*.md]
trim_trailing_whitespace = false
```

- [ ] **Step 7: Write `.gitattributes`**

```
* text=auto eol=lf
*.sh text eol=lf
githooks/* text eol=lf
*.golden text eol=lf
*.png binary
```

- [ ] **Step 8: Replace `.gitignore`**

```
# build outputs
/gaugewire
/gaugewire.exe
/dist/
coverage.out

# editors
.idea/
.vscode/

# personal, never shared
CLAUDE.local.md
/NOTES.local.md
.claude/settings.local.json
.claude/hooks/last-verify.log
```

- [ ] **Step 9: Verify**

```bash
make help
make tools
go build ./...
```

Expected: the help lists every target; `make tools` prints four version lines; `go build ./...` warns that the pattern matched no packages and exits 0, because no package exists yet.

- [ ] **Step 10: Commit**

```bash
git add go.mod tools/go.mod tools/go.sum Makefile .editorconfig .gitattributes .gitignore
git commit -m "$(cat <<'EOF'
build: add go module, pinned tools module and makefile

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Walking skeleton with the `version` command

**Files:**
- Create: `internal/cli/cli.go`, `internal/cli/version.go`, `internal/cli/cli_test.go`, `internal/cli/version_test.go`, `cmd/gaugewire/main.go`, `cmd/gaugewire/main_integration_test.go`

**Interfaces:**
- Produces: `cli.BuildInfo{Version, Commit, Date string}`; `cli.ErrUsage error`; `func cli.Run(args []string, info cli.BuildInfo, stdout io.Writer) error`. Later plans add cases to the `switch` inside `Run` and widen the signature when a command needs stderr. `cmd/gaugewire/main.go` exits 2 on a usage error and 1 on any other error.

- [ ] **Step 1: Write the failing unit tests**

`internal/cli/cli_test.go`:

```go
package cli

import (
	"bytes"
	"testing"
)

// outcome captures everything a command produces, so each test compares one value.
type outcome struct {
	err    string
	stdout string
}

func runCommand(t *testing.T, args []string, info BuildInfo) outcome {
	t.Helper()
	var stdout bytes.Buffer
	err := Run(args, info, &stdout)
	got := outcome{stdout: stdout.String()}
	if err != nil {
		got.err = err.Error()
	}
	return got
}

func TestVersionWritesBuildInfo(t *testing.T) {
	t.Parallel()
	info := BuildInfo{Version: "v1.2.3", Commit: "abc1234", Date: "2026-09-17T10:00:00Z"}
	got := runCommand(t, []string{"version"}, info)
	want := outcome{stdout: "gaugewire v1.2.3\ncommit: abc1234\nbuilt:  2026-09-17T10:00:00Z\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNoArgumentsReturnsUsage(t *testing.T) {
	t.Parallel()
	got := runCommand(t, nil, BuildInfo{})
	want := outcome{err: ErrUsage.Error()}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUnknownCommandReturnsUsage(t *testing.T) {
	t.Parallel()
	got := runCommand(t, []string{"bogus"}, BuildInfo{})
	want := outcome{err: `unknown command "bogus": ` + ErrUsage.Error()}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

`internal/cli/version_test.go`:

```go
package cli

import (
	"runtime/debug"
	"testing"
)

func TestResolveBuildInfoKeepsLinkerValues(t *testing.T) {
	t.Parallel()
	info := BuildInfo{Version: "v1.0.0", Commit: "abc", Date: "2026-09-17"}
	read := func() (*debug.BuildInfo, bool) {
		t.Error("module build info must not be read when the linker set a version")
		return nil, false
	}
	got := resolveBuildInfo(info, read)
	if got != info {
		t.Fatalf("got %+v, want %+v", got, info)
	}
}

func TestResolveBuildInfoFallsBackToModuleInfo(t *testing.T) {
	t.Parallel()
	read := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Main: debug.Module{Version: "v0.1.0"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "deadbeef"},
				{Key: "vcs.time", Value: "2026-09-17T09:00:00Z"},
			},
		}, true
	}
	got := resolveBuildInfo(BuildInfo{Version: "dev", Commit: "none", Date: "unknown"}, read)
	want := BuildInfo{Version: "v0.1.0", Commit: "deadbeef", Date: "2026-09-17T09:00:00Z"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestResolveBuildInfoWithoutModuleInfoKeepsDefaults(t *testing.T) {
	t.Parallel()
	read := func() (*debug.BuildInfo, bool) { return nil, false }
	info := BuildInfo{Version: "dev", Commit: "none", Date: "unknown"}
	got := resolveBuildInfo(info, read)
	if got != info {
		t.Fatalf("got %+v, want %+v", got, info)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL with build errors, `undefined: Run`, `undefined: BuildInfo`, `undefined: ErrUsage`, `undefined: resolveBuildInfo`.

- [ ] **Step 3: Write the implementation**

`internal/cli/cli.go`:

```go
// Package cli implements the gaugewire subcommands.
package cli

import (
	"errors"
	"fmt"
	"io"
)

// BuildInfo describes the running binary. The linker sets these values for a
// release build; a development build falls back to the module build info.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// ErrUsage is returned when the arguments do not name a valid command.
var ErrUsage = errors.New("usage: gaugewire <command>\n\ncommands:\n  version   print version, commit and build date")

// Run executes the command named by args and writes its output to stdout.
func Run(args []string, info BuildInfo, stdout io.Writer) error {
	if len(args) == 0 {
		return ErrUsage
	}
	switch args[0] {
	case "version":
		return runVersion(info, stdout)
	default:
		return fmt.Errorf("unknown command %q: %w", args[0], ErrUsage)
	}
}
```

`internal/cli/version.go`:

```go
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
```

`cmd/gaugewire/main.go`:

```go
// Command gaugewire observes Claude Code subscription quota and publishes it to sinks.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/sulcer/gaugewire/internal/cli"
)

// Set by the linker at release time; see .goreleaser.yaml.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	info := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	err := cli.Run(os.Args[1:], info, os.Stdout)
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, cli.ErrUsage) {
		os.Exit(2)
	}
	os.Exit(1)
}
```

- [ ] **Step 4: Run the unit tests and watch them pass**

Run: `go test -race -shuffle=on -count=1 ./...`
Expected: `ok  github.com/sulcer/gaugewire/internal/cli`.

- [ ] **Step 5: Write the integration test**

`cmd/gaugewire/main_integration_test.go`:

```go
//go:build integration

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// buildBinary compiles the command into a temporary directory and returns its path.
func buildBinary(t *testing.T, ldflags string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "gaugewire")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	args := []string{"build", "-o", binary}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	args = append(args, ".")
	out, err := exec.CommandContext(t.Context(), "go", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return binary
}

func TestBinaryPrintsInjectedVersion(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "-X main.version=v9.9.9 -X main.commit=cafe -X main.date=2026-09-17")
	out, err := exec.CommandContext(t.Context(), binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\n%s", err, out)
	}
	want := "gaugewire v9.9.9\ncommit: cafe\nbuilt:  2026-09-17\n"
	if string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestBinaryExitsTwoOnUsageError(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "")
	err := exec.CommandContext(t.Context(), binary).Run()
	exitErr, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		t.Fatalf("got error %v, want an *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Fatalf("exit code %d, want 2", exitErr.ExitCode())
	}
}
```

- [ ] **Step 6: Run the integration tests**

Run: `go test -race -tags integration -count=1 ./cmd/gaugewire/`
Expected: `ok  github.com/sulcer/gaugewire/cmd/gaugewire`.

- [ ] **Step 7: Commit**

```bash
git add cmd internal
git commit -m "$(cat <<'EOF'
feat: add walking skeleton with version command

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Lint and format configuration

**Files:**
- Create: `.golangci.yml`

**Interfaces:**
- Produces: `make lint` and `make fmt` green on the tree. Every later task keeps them green.

- [ ] **Step 1: Write `.golangci.yml`**

```yaml
version: "2"

run:
  timeout: 5m
  tests: true

linters:
  default: standard
  enable:
    - bodyclose
    - depguard
    - errname
    - errorlint
    - exhaustive
    - forbidigo
    - gocritic
    - gosec
    - intrange
    - misspell
    - nilerr
    - noctx
    - perfsprint
    - revive
    - thelper
    - tparallel
    - unconvert
    - unparam
    - usestdlibvars
    - usetesting
    - wastedassign
  settings:
    depguard:
      rules:
        main:
          deny:
            - pkg: github.com/sirupsen/logrus
              desc: use log/slog
            - pkg: github.com/spf13/viper
              desc: configuration is encoding/json
            - pkg: github.com/pkg/errors
              desc: use fmt.Errorf with %w
            - pkg: github.com/stretchr/testify
              desc: use the standard testing package
    forbidigo:
      forbid:
        - pattern: ^fmt\.Print(f|ln)?$
          msg: write to the io.Writer the command received, never to package-level stdout
    govet:
      enable-all: true
      disable:
        - fieldalignment
    revive:
      rules:
        - name: exported
        - name: package-comments
  exclusions:
    generated: lax
    presets:
      - std-error-handling
      - common-false-positives
    rules:
      - path: _test\.go
        linters:
          - gosec
          - forbidigo
          - noctx

formatters:
  enable:
    - gofumpt
    - gci
  settings:
    gofumpt:
      extra-rules: true
    gci:
      sections:
        - standard
        - default
        - localmodule
```

- [ ] **Step 2: Verify the configuration**

Run: `go tool -modfile=tools/go.mod golangci-lint config verify`
Expected: exit 0. If this version rejects a linter or setting name, delete that entry and say which one in the commit body. The scaffold spec lists this set as candidates validated here.

- [ ] **Step 3: Format, then lint**

Run: `make fmt && make fmt-check && make vet && make lint`
Expected: `fmt-check` prints nothing and `lint` exits 0. Fix any finding in the Task 2 code rather than excluding it.

- [ ] **Step 4: Run the tests again**

Run: `make check`
Expected: all green, since `fmt` may have reordered imports.

- [ ] **Step 5: Commit**

```bash
git add .golangci.yml cmd internal
git commit -m "$(cat <<'EOF'
build: add golangci-lint v2 configuration

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Git hooks

**Files:**
- Create: `githooks/pre-commit`, `githooks/commit-msg`

**Interfaces:**
- Consumes: the format, vet and lint commands from Tasks 1 and 3.
- Produces: `make setup` activates both hooks on a machine.

- [ ] **Step 1: Write `githooks/pre-commit`**

```sh
#!/bin/sh
# Pre-commit gate: format check, vet and lint whenever Go or module files are staged.
set -eu

staged=$(git diff --cached --name-only --diff-filter=ACMR -- '*.go' 'go.mod' 'go.sum' 'tools/go.mod' 'tools/go.sum' || true)
if [ -z "$staged" ]; then
  exit 0
fi

go_files=$(printf '%s\n' "$staged" | grep '\.go$' || true)
if [ -n "$go_files" ]; then
  # shellcheck disable=SC2086
  unformatted=$(go tool -modfile=tools/go.mod gofumpt -l $go_files)
  if [ -n "$unformatted" ]; then
    printf 'pre-commit: these files are not gofumpt-formatted:\n%s\nrun: make fmt\n' "$unformatted" >&2
    exit 1
  fi
fi

go vet ./...
go tool -modfile=tools/go.mod golangci-lint run
```

- [ ] **Step 2: Write `githooks/commit-msg`**

```sh
#!/bin/sh
# Commit-msg gate: Conventional Commits without a scope on the first line.
set -eu

first=$(sed -n '1p' "$1")

case "$first" in
  "Merge "*|"Revert "*|"fixup! "*|"squash! "*) exit 0 ;;
esac

if printf '%s\n' "$first" | grep -Eq '^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)!?: [a-z].{0,71}$'; then
  exit 0
fi

cat >&2 <<EOF
commit-msg: the first line must be a Conventional Commit without a scope:
  <type>: <lowercase subject, at most 72 characters>
  types: feat fix docs style refactor perf test build ci chore revert
got: $first
EOF
exit 1
```

- [ ] **Step 3: Make them executable and activate them**

```bash
chmod +x githooks/pre-commit githooks/commit-msg
make setup
git config core.hooksPath
```

Expected: the last command prints `githooks`.

- [ ] **Step 4: Verify the commit-msg gate**

```bash
printf 'feat(cli): add thing\n' > /tmp/msg-scoped; sh githooks/commit-msg /tmp/msg-scoped; echo "exit=$?"
printf 'feat: add thing\n' > /tmp/msg-ok; sh githooks/commit-msg /tmp/msg-ok; echo "exit=$?"
printf 'Add thing\n' > /tmp/msg-bad; sh githooks/commit-msg /tmp/msg-bad; echo "exit=$?"
rm -f /tmp/msg-scoped /tmp/msg-ok /tmp/msg-bad
```

Expected: `exit=1` for the scoped message with the explanation, `exit=0` for the valid one, `exit=1` for the sentence-case one.

- [ ] **Step 5: Verify the pre-commit gate rejects an unformatted file**

Run the hook script directly, so no throwaway commit is created:

```bash
printf 'package cli\n\nfunc  unformatted() {}\n' > internal/cli/scratch.go
git add internal/cli/scratch.go
sh githooks/pre-commit; echo "exit=$?"
git restore --staged internal/cli/scratch.go
rm internal/cli/scratch.go
git status --short
```

Expected: `pre-commit: these files are not gofumpt-formatted`, `exit=1`, and a clean status afterwards.

- [ ] **Step 6: Commit**

```bash
git add githooks
git commit -m "$(cat <<'EOF'
build: add git hooks for format, vet, lint and commit messages

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

Expected: the new hooks run during this very commit and pass.

---

### Task 5: Claude Code settings and hooks

**Files:**
- Create: `.claude/settings.json`, `.claude/hooks/verify.sh`

**Interfaces:**
- Consumes: the `go tool -modfile=tools/go.mod gofumpt` invocation from Task 1.
- Produces: automatic formatting of every Go file an agent edits, and a build-and-test report at the end of every agent turn.

- [ ] **Step 1: Write `.claude/settings.json`**

```json
{
  "permissions": {
    "allow": [
      "Bash(go build:*)",
      "Bash(go test:*)",
      "Bash(go vet:*)",
      "Bash(go tool:*)",
      "Bash(go mod:*)",
      "Bash(go run:*)",
      "Bash(go doc:*)",
      "Bash(make:*)",
      "Bash(git status:*)",
      "Bash(git diff:*)",
      "Bash(git log:*)"
    ],
    "deny": [
      "Read(.env)",
      "Read(.env.*)",
      "Read(**/*.key)"
    ]
  },
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "if": "Edit(*.go)",
            "command": "go",
            "args": ["tool", "-modfile", "${CLAUDE_PROJECT_DIR}/tools/go.mod", "gofumpt", "-w", "${file_path}"],
            "timeout": 30
          },
          {
            "type": "command",
            "if": "Write(*.go)",
            "command": "go",
            "args": ["tool", "-modfile", "${CLAUDE_PROJECT_DIR}/tools/go.mod", "gofumpt", "-w", "${file_path}"],
            "timeout": 30
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "sh \"${CLAUDE_PROJECT_DIR}\"/.claude/hooks/verify.sh",
            "timeout": 180
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 2: Write `.claude/hooks/verify.sh`**

```sh
#!/bin/sh
# Stop hook: build and run the unit tests. Never blocks. On failure it writes the
# output to .claude/hooks/last-verify.log and tells the agent to read it.
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
log=.claude/hooks/last-verify.log
if { go build ./... && go test ./...; } >"$log" 2>&1; then
  rm -f "$log"
  exit 0
fi
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"Stop","additionalContext":"go build or go test failed. Read .claude/hooks/last-verify.log, fix the cause, then run make check."}}'
exit 0
```

- [ ] **Step 3: Verify the stop hook by hand**

```bash
chmod +x .claude/hooks/verify.sh
sh .claude/hooks/verify.sh; echo "exit=$?"; ls .claude/hooks
printf 'package cli\n\nfunc broken() int { return "not an int" }\n' > internal/cli/broken.go
sh .claude/hooks/verify.sh; echo "exit=$?"; head -3 .claude/hooks/last-verify.log
rm internal/cli/broken.go .claude/hooks/last-verify.log
```

Expected: the first run prints nothing, exits 0 and leaves no log; the second prints the JSON line, exits 0, and the log holds the compile error.

- [ ] **Step 4: Verify the edit hook inside a Claude Code session**

Start `claude` in the repository, ask it to add a blank line inside `internal/cli/version.go`, then run `make fmt-check`. Expected: clean, because the hook reformatted the file. If the hook does not fire, run `claude --debug` and look for its startup line; if `Write(*.go)` is rejected as a rule, delete that second entry and keep the `Edit(*.go)` one, which covers both tools.

- [ ] **Step 5: Commit**

```bash
git add .claude/settings.json .claude/hooks/verify.sh
git commit -m "$(cat <<'EOF'
chore: add claude code permissions, format hook and verify hook

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Rulebook and entry documents

**Files:**
- Create: `AGENTS.md`, `CLAUDE.md`, `README.md`, `CONTRIBUTING.md`, `.claude/rules/always-on/workflow.md`, `.claude/rules/always-on/adr-and-spec-discipline.md`, `.claude/rules/always-on/simplicity.md`, `.claude/rules/code/go-code.md`, `.claude/rules/code/go-testing.md`, `.claude/rules/code/dependencies.md`, `.claude/rules/code/ci-and-release.md`

**Interfaces:**
- Consumes: the Makefile targets from Task 1 and the hooks from Tasks 4 and 5.
- Produces: the rulebook every later session loads.

- [ ] **Step 1: Write `AGENTS.md`**

````markdown
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
internal/renderer/       the user's previous status-line command
internal/sink/           Sink and Router; internal/sink/databox/ is the first sink
internal/config/         config.json
fixtures/statusline/     real and synthetic stdin payloads
tools/                   pinned dev tools
githooks/                pre-commit, commit-msg
scripts/                 coverage gate
docs/                    adr, spec, plans, how-tos, research
```

Dependency direction: `source → quota`, `quota → nothing`, `store, sink → quota`,
`sink/databox → sink, quota`, `cli → everything`. Nothing imports `cli`.

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
hooks in `.claude/settings.json` format every Go file you edit and run build and tests when a
turn ends. A subagent does not inherit these rules: hand it the rule files whose paths match
what it will touch, plus the spec page.
````

- [ ] **Step 2: Write `CLAUDE.md`**

```markdown
@AGENTS.md

## Claude Code

- Run `/code-review` on the branch before opening a pull request.
- When the stop hook reports a failure, read `.claude/hooks/last-verify.log` before anything else.
```

- [ ] **Step 3: Write `.claude/rules/always-on/workflow.md`**

```markdown
# Workflow

> **Always-on rule.** How to work in this repository.

## Read first

`AGENTS.md`, then the `docs/spec/` page for the area you touch, then any ADR it links. Do not
read `docs/research/`, `dist/` or `coverage.out`.

## Ask or proceed

Proceed when a rule, a spec page or an existing sibling answers the question. Ask when two rules
conflict, when a spec is silent about a contract, when a new dependency seems necessary, or
before any destructive or outward-facing action: pushing, releasing, deleting outside the tree.
If a rule is wrong, propose an ADR rather than breaking it quietly.

## Gate

Test-driven: write the failing test, watch it fail, make it pass, run `make check`. Every commit
passes the pre-commit hook; never bypass it with `--no-verify`. Commits and pushes happen only
with the owner's approval, unless autonomous mode was granted for that session.

## Boundaries

- Third-party behaviour is relied on only when its public documentation states it. Anything
  learned another way is written down as an assumption to verify, never as a fact.
- Never read `.env*` or `*.key`. Never log or persist a credential.
- Never modify `~/.claude/settings.json` except through `gaugewire install` and `uninstall`.

## Subagents

A subagent does not inherit these rules. Give it the rule files whose `paths` match what it will
touch, plus the relevant spec page. Verify its work yourself; verification is not delegated.
```

- [ ] **Step 4: Write `.claude/rules/always-on/adr-and-spec-discipline.md`**

```markdown
# ADR and spec discipline

> **Always-on rule.** Applies to every behaviour-changing change and every documentation edit.

## ADRs

A decision someone would otherwise re-litigate gets `docs/adr/YYYY-MM-DD-kebab-title.md`, with
frontmatter `tags`, `status: accepted|superseded`, `decision-date`, and these sections in order:
Title, Members, Status, Context and Problem Statement, Options considered, Decision,
Consequences, and optionally Out of scope. Every section earns its place or gets one line. Add a
row to `docs/adr/README.md`. Never delete an ADR. To supersede, set the old file's
`status: superseded` and add `Superseded by: <file>` right after its title, and put
`Supersedes: <file>` in the same position in the new one. When only some decisions change, amend
instead: `Amended by: <file> (decision N)` on the old, `Amends: <file> (decision N)` on the new,
and say explicitly which decisions still stand.

## Specs

`docs/spec/<topic>/` states the current design as fact. Every page opens with
`Status: Draft|Stable · Built|Partial|Planned · date · one sentence`. The build state is a claim
about the tree: check it before writing it, and flip it in the pull request that builds the
thing. A `Stable` spec is still edited in place, but every behaviour-changing edit also gets an
ADR and a `Changes: <adr-file>` back-link. Every flow gets a Mermaid diagram; prose accompanies
the diagram and never replaces it.

## Plans

`docs/plans/YYYY-MM-DD-slug.md`, committed on the feature branch, kept current as pull requests
land, and deleted in the final pull request of the feature, after anything durable has moved to
an ADR, a spec or `docs/nice-to-have.md`.
```

- [ ] **Step 5: Write `.claude/rules/always-on/simplicity.md`**

```markdown
# Simplicity

> **Always-on rule.** Applies to every design, plan and code change.

The boring, direct solution that satisfies the requirement in front of you wins. Indirection
must earn its place: no interface with one implementation, no factory for one product, no
configuration knob for a constant, no scaffolding for later. Prefer the standard library, then a
platform feature, then a dependency already present, then new code. When a task seems to need
the complex version, present the simple and the complex versions with their trade-offs and let
the reviewer choose. Simplicity governs how, never whether: it never overrides correctness,
validation at a trust boundary, error handling that prevents data loss, or the gate.
```

- [ ] **Step 6: Write `.claude/rules/code/go-code.md`**

```markdown
---
paths:
  - "cmd/**/*.go"
  - "internal/**/*.go"
---

# Go code

> **Path-scoped:** loads when you touch Go source.

- Errors: `fmt.Errorf("...: %w", err)`; a sentinel is `var ErrX = errors.New(...)` in the owning
  package; match with `errors.Is` and `errors.AsType`. No panic outside `main`. No `os.Exit`
  outside `cmd/gaugewire/main.go`; commands return errors.
- `context.Context` is the first parameter of anything doing I/O, and every network call has a
  timeout.
- Logging is `log/slog` only. A command writes to the `io.Writer` it was given, never to
  package-level stdout.
- Package layout and dependency direction are in `AGENTS.md`. Platform differences live in
  build-tagged files, `_unix.go` and `_windows.go`.
- Names follow Effective Go: short receivers, no stutter, every exported identifier documented
  with a sentence that starts with its name. No acronym a reader would have to look up.
- Comments explain why. They never narrate the diff, the history, or an issue number.
- Every file write is temporary file, fsync, rename. Every lock is an advisory file lock.
```

- [ ] **Step 7: Write `.claude/rules/code/go-testing.md`**

```markdown
---
paths:
  - "**/*_test.go"
  - "testdata/**"
  - "fixtures/**"
---

# Go tests

> **Path-scoped:** loads when you touch tests, test data or fixtures.

- Standard `testing` package. Table-driven when there are several cases. `t.Parallel()` in every
  test unless it mutates process state. `t.Context()`, `t.TempDir()`, `t.Setenv()`.
- One assertion per test: build the whole expected value and compare it once, with `==` for
  comparable structs or `cmp.Diff` otherwise. Never assert field by field.
- An expected value comes from the spec, a fixture, or arithmetic a reader can follow. It never
  comes from running the code under test and pasting what it printed.
- Golden files live in `testdata/` and are regenerated only with the diff shown to a human.
- Time-dependent logic uses `testing/synctest`. Network is `httptest`; no test contacts a real
  service. A test that spawns the built binary carries `//go:build integration`.
- A test name states the behaviour: `TestReduceKeepsStateWhenWindowAbsent`.
```

- [ ] **Step 8: Write `.claude/rules/code/dependencies.md`**

```markdown
---
paths:
  - "go.mod"
  - "go.sum"
  - "tools/**"
---

# Dependencies

> **Path-scoped:** loads when you touch a module file.

- Standard library first. The allowed runtime dependency is `github.com/gofrs/flock`; the
  allowed test dependency is `github.com/google/go-cmp`. Anything else needs an ADR that names
  the standard-library alternative and why it does not do.
- Pin exact versions. Run `go tool -modfile=tools/go.mod govulncheck ./...` after any change and
  state the result in the pull request.
- Dev tools live only in `tools/go.mod` as `tool` directives, never as `go install ...@latest`.
- Dependabot cooldowns are configured in `.github/dependabot.yml`; do not merge a bump early.
- `go mod tidy -diff` must be clean for both modules.
```

- [ ] **Step 9: Write `.claude/rules/code/ci-and-release.md`**

```markdown
---
paths:
  - ".github/**"
  - ".goreleaser.yaml"
  - "Makefile"
  - "githooks/**"
  - "scripts/**"
---

# CI, hooks and release

> **Path-scoped:** loads when you touch automation.

- Every gate is one command: the Makefile wraps it, the git hook runs it, CI runs it. Never add
  a check in one place only.
- CI calls `go` directly, because Windows runners may lack `make`.
- Actions are pinned by commit SHA with the version in a trailing comment. Every workflow
  declares the least permission it needs.
- Hook scripts are POSIX `sh` without `jq` or `python`, so they run under Git for Windows.
- Releases are `v*` tags; goreleaser builds five binaries. Before 1.0, a breaking change bumps
  the minor version. The changelog is built from commit types, so keep subjects meaningful.
```

- [ ] **Step 10: Write `README.md`**

```markdown
# Gaugewire

Lightweight quota observability for a fleet of Claude Code machines. Gaugewire installs itself
as Claude Code's status-line command, reads the documented five-hour and seven-day quota fields,
keeps one durable state per machine, and publishes normalized events to a sink. Databox is the
first sink.

> Claude Code owns quota discovery. Gaugewire owns normalization, local durability and delivery.
> Sinks own storage and visualization.

## Status

Scaffold only. The `version` command exists; the observer is specified in
[`docs/spec/gaugewire/`](docs/spec/gaugewire/README.md) and arrives in later plans.

## Build

```sh
git clone git@github.com:sulcer/gaugewire.git
cd gaugewire
make build
./gaugewire version
```

Go 1.27 is required; the toolchain downloads itself.

## Develop

```sh
make setup      # git hooks and pinned tools
make check      # format check, vet, lint, unit tests
make ci         # everything CI runs
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and the rulebook in [`AGENTS.md`](AGENTS.md).

## Documentation

[`docs/README.md`](docs/README.md) is the index: decisions in `docs/adr/`, the living design in
`docs/spec/`, procedures in `docs/how-tos/`.
```

- [ ] **Step 11: Write `CONTRIBUTING.md`**

```markdown
# Contributing

1. Read [`AGENTS.md`](AGENTS.md). It is the rulebook for humans and agents alike.
2. Run `make setup` once per machine. It installs the git hooks and warms the pinned tools.
3. Work on a branch named `<type>/<topic>`. Commits are Conventional Commits without a scope.
4. Run `make check` before every commit; the hooks run it anyway. Run `make ci` before opening a
   pull request.
5. A behaviour change gets an ADR in `docs/adr/`. A spec page that now describes built behaviour
   flips its marker in the same pull request.
6. Open the pull request with the template. `main` moves only through pull requests.
```

- [ ] **Step 12: Verify the links and the always-on budget**

```bash
for f in AGENTS.md README.md CONTRIBUTING.md CLAUDE.md; do
  grep -o '](\([^)]*\.md\)[^)]*)' "$f" | sed 's/](//; s/[)#].*//' | while read -r p; do
    [ -e "$p" ] || echo "BROKEN in $f: $p"
  done
done
wc -l .claude/rules/always-on/*.md | tail -1
```

Expected: no `BROKEN` line, and the always-on total under 200 lines.

- [ ] **Step 13: Commit**

```bash
git add AGENTS.md CLAUDE.md README.md CONTRIBUTING.md .claude/rules
git commit -m "$(cat <<'EOF'
docs: add rulebook, scoped rules and entry documents

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: CI, repository automation and the coverage gate

**Files:**
- Create: `scripts/coverage-gate.sh`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.github/dependabot.yml`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/CODEOWNERS`

**Interfaces:**
- Consumes: every command from Tasks 1 to 3, and the goreleaser configuration from Task 8. The `snapshot` job stays red until Task 8 lands, which is expected on the first push.
- Produces: green checks on the pull request.

- [ ] **Step 1: Write `scripts/coverage-gate.sh`**

```sh
#!/bin/sh
# Hard coverage gate for the pure packages. Everything else is reported, not gated.
# A package that does not exist yet is skipped, so the gate works from the first commit.
set -eu

minimum=90
status=0
for pkg in ./internal/quota ./internal/source/claude; do
  if [ ! -d "$pkg" ]; then
    echo "coverage-gate: $pkg not present, skipped"
    continue
  fi
  line=$(go test -cover -count=1 "$pkg" | grep -o 'coverage: [0-9.]*%' || true)
  pct=${line#coverage: }
  pct=${pct%\%}
  echo "coverage-gate: $pkg ${pct:-0}%"
  if awk -v p="${pct:-0}" -v m="$minimum" 'BEGIN { exit !(p < m) }'; then
    echo "coverage-gate: $pkg is below ${minimum}%" >&2
    status=1
  fi
done
exit $status
```

- [ ] **Step 2: Run the gate**

Run: `chmod +x scripts/coverage-gate.sh && make cover`
Expected: a `total:` line from `go tool cover`, two `not present, skipped` lines, exit 0.

- [ ] **Step 3: Write `.github/workflows/ci.yml`**

```yaml
name: ci

on:
  pull_request:
  push:
    branches: [main]
  schedule:
    - cron: "0 6 * * 1"
  workflow_dispatch:

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

jobs:
  lint:
    if: github.event_name != 'schedule'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: |
            go.sum
            tools/go.sum
      - run: go mod tidy -diff
      - run: cd tools && go mod tidy -diff
      - run: test -z "$(go tool -modfile=tools/go.mod gofumpt -l .)"
      - run: go vet ./...
      - run: go tool -modfile=tools/go.mod golangci-lint config verify
      - run: go tool -modfile=tools/go.mod golangci-lint run

  test:
    if: github.event_name != 'schedule'
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: |
            go.sum
            tools/go.sum
      - run: go test -race -shuffle=on -count=1 ./...
      - run: go test -race -tags integration -count=1 ./...

  test-macos:
    if: github.event_name == 'push' || github.event_name == 'workflow_dispatch'
    runs-on: macos-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: |
            go.sum
            tools/go.sum
      - run: go test -race -shuffle=on -count=1 ./...
      - run: go test -race -tags integration -count=1 ./...

  coverage:
    if: github.event_name != 'schedule'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: |
            go.sum
            tools/go.sum
      - run: go test -coverprofile=coverage.out -covermode=atomic ./...
      - run: go tool cover -func=coverage.out | tail -1
      - run: sh scripts/coverage-gate.sh

  vuln:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: |
            go.sum
            tools/go.sum
      - run: go tool -modfile=tools/go.mod govulncheck ./...

  snapshot:
    if: github.event_name != 'schedule'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: |
            go.sum
            tools/go.sum
      - run: go tool -modfile=tools/go.mod goreleaser release --snapshot --clean
      - run: ls -R dist
```

If the Windows job fails because the race detector needs a C compiler that the runner image
does not provide, drop `-race` from the Windows matrix entry only and note it in the commit
body; every other job keeps it.

- [ ] **Step 4: Write `.github/workflows/release.yml`**

```yaml
name: release

on:
  push:
    tags: ["v*"]

permissions:
  contents: read

jobs:
  test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, windows-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: |
            go.sum
            tools/go.sum
      - run: go test -race -shuffle=on -count=1 ./...
      - run: go test -race -tags integration -count=1 ./...

  goreleaser:
    needs: test
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: |
            go.sum
            tools/go.sum
      - run: go tool -modfile=tools/go.mod goreleaser release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 5: Write `.github/dependabot.yml`**

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
    cooldown:
      default-days: 7
      semver-major-days: 14
      semver-minor-days: 7
      semver-patch-days: 3
  - package-ecosystem: gomod
    directory: /tools
    schedule:
      interval: weekly
    cooldown:
      default-days: 7
      semver-major-days: 14
      semver-minor-days: 7
      semver-patch-days: 3
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
    cooldown:
      default-days: 7
```

- [ ] **Step 6: Write `.github/PULL_REQUEST_TEMPLATE.md`**

```markdown
## Summary

- verb-first bullet on why this change exists

## Test plan

- [ ] `make ci` passes locally

---

- [ ] ADR added if behaviour changed, with its row in `docs/adr/README.md`
- [ ] Spec build markers flipped for anything this pull request builds
- [ ] Third-party behaviour is cited from public documentation or marked as an assumption to verify
```

- [ ] **Step 7: Write `.github/CODEOWNERS`**

```
* @sulcer
```

- [ ] **Step 8: Commit**

```bash
git add scripts .github
git commit -m "$(cat <<'EOF'
ci: add lint, test, coverage, vulnerability and snapshot workflows

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 9: With the owner's approval, push and open a draft pull request**

```bash
git push -u origin chore/repo-scaffold
gh pr create --draft --assignee sulcer --label patch --title "chore: add repository scaffold" --body "$(cat <<'EOF'
## Summary

- Pin the Go toolchain and every dev tool so local and CI runs use identical binaries
- Make each mechanical gate one command, run by a git hook, a Claude Code hook and CI
- Add the rulebook, the scoped rules and the entry documents
- Add a walking-skeleton binary so the pipeline has something real to build and release

## Test plan

- [ ] `make ci` passes locally
- [ ] lint, test on Ubuntu and Windows, coverage, vulnerability and snapshot jobs pass
EOF
)"
gh pr checks --watch
```

Expected: `lint`, `test (ubuntu-latest)`, `test (windows-latest)`, `coverage` and `vuln` pass;
`snapshot` fails until Task 8 lands.

---

### Task 8: Release configuration

**Files:**
- Create: `.goreleaser.yaml`

**Interfaces:**
- Consumes: `main.version`, `main.commit` and `main.date` from Task 2.
- Produces: `dist/` output from `make snapshot`, and a green `snapshot` job.

- [ ] **Step 1: Write `.goreleaser.yaml`**

```yaml
version: 2

project_name: gaugewire

builds:
  - id: gaugewire
    main: ./cmd/gaugewire
    binary: gaugewire
    env:
      - CGO_ENABLED=0
    goos:
      - darwin
      - linux
      - windows
    goarch:
      - amd64
      - arm64
    ignore:
      - goos: windows
        goarch: arm64
    mod_timestamp: "{{ .CommitTimestamp }}"
    flags:
      - -trimpath
    ldflags:
      - -s -w -X main.version={{ .Version }} -X main.commit={{ .Commit }} -X main.date={{ .CommitDate }}

archives:
  - id: default
    formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format_overrides:
      - goos: windows
        formats: [zip]

checksum:
  name_template: checksums.txt

changelog:
  sort: asc
  use: git
  groups:
    - title: Features
      regexp: '^feat!?:'
      order: 0
    - title: Fixes
      regexp: '^fix!?:'
      order: 1
    - title: Performance
      regexp: '^perf!?:'
      order: 2
    - title: Other
      order: 999
  filters:
    exclude:
      - "^docs:"
      - "^chore:"
      - "^ci:"
      - "^test:"
      - "^build:"
      - "^style:"
      - "^refactor:"

release:
  prerelease: auto
```

- [ ] **Step 2: Check the configuration**

Run: `go tool -modfile=tools/go.mod goreleaser check`
Expected: valid. Fix any key this version rejects before continuing.

- [ ] **Step 3: Build a snapshot and read the injected version back**

```bash
make snapshot
ls dist
"$(find dist -path '*darwin_arm64*' -name gaugewire | head -1)" version
```

Expected: five binaries (darwin amd64 and arm64, linux amd64 and arm64, windows amd64), a
`checksums.txt`, and a version line ending in `-SNAPSHOT-<short sha>` with the commit and date
filled in. On Linux use the `linux_amd64` path instead.

- [ ] **Step 4: Commit and push**

```bash
git add .goreleaser.yaml
git commit -m "$(cat <<'EOF'
build: add goreleaser configuration

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

With the owner's approval: `git push`, then `gh pr checks --watch`.
Expected: every check green, `snapshot` included.

---

### Task 9: Flip the markers and close the plan

**Files:**
- Modify: `docs/spec/repo-scaffold/README.md`, `docs/spec/README.md`
- Delete: `docs/plans/2026-09-17-repo-scaffold.md`

- [ ] **Step 1: Flip the scaffold spec to built**

Change line 3 of `docs/spec/repo-scaffold/README.md` to:

```
Status: Stable · Built · 2026-09-17 · How the Gaugewire repository is built, gated, released and made legible to humans and AI agents.
```

Read the page once more and correct any sentence that no longer matches the tree, for example a
linter dropped in Task 3 or a `-race` flag dropped for Windows in Task 7. A spec states the
current state as fact.

- [ ] **Step 2: Update the spec index**

In `docs/spec/README.md`, change the `repo-scaffold` row's markers to `Stable` and `Built`.

- [ ] **Step 3: Delete this plan**

Everything durable it established already lives in the ADRs and the scaffold spec.

```bash
git rm docs/plans/2026-09-17-repo-scaffold.md
```

- [ ] **Step 4: Commit, then finish the branch**

```bash
git add docs/spec
git commit -m "$(cat <<'EOF'
docs: mark the repository scaffold as built

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

With the owner's approval: `git push`, then `gh pr ready`, then merge. Afterwards, if the owner
agrees, protect `main`: require a pull request and the `lint`, `test`, `coverage`, `vuln` and
`snapshot` checks.
