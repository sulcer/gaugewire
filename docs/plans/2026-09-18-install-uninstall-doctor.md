# Install, Uninstall and Doctor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Gaugewire installable on a machine: `gaugewire install` splices only the `statusLine` value of Claude Code's settings file, `gaugewire uninstall` restores it byte for byte, and `gaugewire doctor` runs every offline health check with a `HEALTHY`/`UNHEALTHY` verdict and exit code 1 on failure. The three sink checks (auth, datasets, last ingestion) arrive with the Databox sink.

**Architecture:** A new pure package `internal/settings` edits one top-level member of a JSON object by byte offsets taken from `encoding/json`'s decoder, so every other byte of the user's file survives; install and uninstall are thin commands on top of it and `internal/config`. `store` gains a dead-letter reader that tolerates quarantined files. `doctor` is a list of small check functions rendered by one function, with the settings path injectable so no test ever touches the real Claude Code settings file.

**Tech Stack:** Go 1.27 standard library (`encoding/json` v1, `os/exec`, `flag`, `uuid`), existing deps only.

**Spec:** `docs/spec/gaugewire/cli-and-install.md` (Commands, Install, Uninstall, doctor, Home directory), `docs/spec/gaugewire/data-contract.md` (config.json `install`), `docs/spec/gaugewire/hot-path.md` (rule 3, Windows shell), `docs/spec/gaugewire/spool-and-flush.md` (dead letters), `docs/spec/gaugewire/testing-strategy.md`. Public third-party documentation this plan relies on: Claude Code settings reference (`statusLine` object: `type`, `command`, optional `padding`, `refreshInterval`, `hideVimModeIndicator`; `disableAllHooks` outside managed settings disables the status line), Claude Code status line page (user settings at `~/.claude/settings.json`; on Windows the command runs through Git Bash when installed else PowerShell; forward slashes in paths), Claude Code settings page (precedence: managed, command line, `.claude/settings.local.json`, `.claude/settings.json`, `~/.claude/settings.json`). Cite these pages as `https://code.claude.com/docs/en/settings-reference`, `https://code.claude.com/docs/en/statusline`, `https://code.claude.com/docs/en/settings` where a spec sentence depends on them.

## Global Constraints

- Branch `feat/install-and-doctor` from the head of `feat/hot-path-and-flusher` (Plan 1b, PR #3). Conventional Commits without scope, first line ≤ 72 characters, `Co-Authored-By: Claude <model> <noreply@anthropic.com>` trailer via the heredoc form. `make check` before every commit; git hooks are active and never bypassed.
- **Commit authority:** commits on this branch are pre-approved for this plan's execution; never push.
- No new dependencies: standard library plus `github.com/gofrs/flock` and `github.com/google/go-cmp` (tests).
- Dependency direction: `settings → nothing`; `config, renderer, logging → store/quota`; `sink → store, quota`; `cli → everything`. Nothing imports `cli`.
- **No test, subagent or command run during this plan may read or write the real `~/.claude/settings.json`.** Every command takes `--settings <path>`; every test passes a temp file. The default path is computed by a pure function and never opened in a test.
- Settings files can hold secrets (`env`, tokens): never print or log their content; print only paths and the `statusLine` command strings. Backups are written with mode 0600.
- Never log or persist a credential. Nothing in the repo may mention internal services, repositories, code or tooling of any third party; grep `git grep -n -i -E 'internal databox|databox services|horizon|environments-api|ingestion-api|~/databox|marketplace' -- ':!docs/research'` before every commit and expect no output.
- Tests: `t.Parallel()` unless `t.Setenv` is used; one whole-value assertion per test (a composite assertion on one outcome value counts as one); expectations from this plan and the fixtures, never from running the code. Tests that need `/bin/sh` or `cat` skip on Windows.
- Files are created with the Write tool; Go files are gofumpt-formatted by the edit hook.
- Third-party behaviour is relied on only when its public documentation states it; anything else is written as an assumption to verify.

## Before you start

```bash
git switch feat/hot-path-and-flusher && git pull --ff-only
git switch -c feat/install-and-doctor
```

If PR #3 has merged by then, branch from `main` instead.

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/settings/settings.go`, `settings_test.go`, `testdata/*.json`, `testdata/*.golden` | `Get`, `Set`, `Delete` on one top-level member by byte offsets; `Load`, `DefaultPath` |
| `internal/store/deadletter.go`, `deadletter_test.go` | `ListDeadLetters` tolerant of `.unreadable` files |
| `internal/source/claude/parse.go` (modify), `parse_test.go` (modify) | export `VersionSupported` |
| `internal/cli/install.go`, `install_test.go` | `gaugewire install` |
| `internal/cli/uninstall.go`, `uninstall_test.go` | `gaugewire uninstall [--purge]` |
| `internal/cli/doctor.go`, `doctor_checks.go`, `doctor_test.go`, `testdata/doctor_*.golden` | `gaugewire doctor` |
| `internal/cli/status.go` (modify), `status_test.go`, `testdata/status_deadletters.golden` | newest dead-letter reason |
| `internal/cli/cli.go` (modify) | three new cases and usage text |
| `cmd/gaugewire/main_integration_test.go` (modify) | install → statusline → uninstall round trip, doctor exit code |
| `docs/spec/gaugewire/cli-and-install.md`, `architecture.md`, `AGENTS.md`, `docs/nice-to-have.md`, `docs/how-tos/install-on-a-machine.md` | markers, layout, how-to |

---

### Task 1: The settings splice engine

**Files:**
- Create: `internal/settings/settings.go`, `internal/settings/settings_test.go`, `internal/settings/testdata/` (inputs and goldens listed below)

**Interfaces:**
- Consumes: nothing.
- Produces: `settings.ErrNotObject`; `settings.Member{Found bool; Value json.RawMessage}` (offsets unexported); `settings.Get(object []byte, key string) (Member, error)`; `settings.Set(object []byte, key string, value json.RawMessage) ([]byte, error)` (replace in place, or append as the last member); `settings.Delete(object []byte, key string) ([]byte, error)` (a no-op returning the input when the key is absent); `settings.Load(path string) (data []byte, existed bool, err error)` (missing file → `[]byte("{}")`, false); `settings.DefaultPath() (string, error)` (`<user home>/.claude/settings.json`).

Invariants the tests pin: `Set` on an existing member changes only the value's bytes; `Set` on a missing member appends `,<indent>"key": value` after the last member (or `{<indent>"key": value<newline>}` on an empty object), where `<indent>` is the whitespace found between `{` and the first key (fallback `"\n  "`), and the separator is `": "` when that whitespace contains a newline, else `":"`; `Delete` of a member that `Set` appended returns the original bytes exactly; `Delete` of a first or middle member removes the key, the value, the following comma and the whitespace up to the next key.

- [ ] **Step 1: Write the fixtures**

`internal/settings/testdata/empty.json`: `{}` (no trailing newline).

`internal/settings/testdata/none.json`:
```json
{
  "model": "claude-sonnet-5",
  "permissions": {
    "allow": ["Bash(git diff *)"]
  }
}
```

`internal/settings/testdata/only.json`:
```json
{
  "statusLine": { "type": "command", "command": "bash ~/.claude/statusline-command.sh", "refreshInterval": 5 }
}
```

`internal/settings/testdata/first.json`:
```json
{
  "statusLine": {"type":"command","command":"~/.claude/statusline.sh"},
  "model": "claude-sonnet-5"
}
```

`internal/settings/testdata/middle.json`:
```json
{
  "model": "claude-sonnet-5",
  "statusLine": {
    "type": "command",
    "command": "jq -r '.model.display_name'",
    "padding": 2,
    "hideVimModeIndicator": true
  },
  "cleanupPeriodDays": 20
}
```

`internal/settings/testdata/last.json` (tabs, CRLF line endings; write the file with `\r\n` and a tab indent):
```
{\r\n\t"model": "claude-sonnet-5",\r\n\t"statusLine": {"type": "command", "command": "powershell -NoProfile -File C:/Users/me/.claude/statusline.ps1"}\r\n}\r\n
```

`internal/settings/testdata/compact.json`: `{"model":"claude-sonnet-5","statusLine":{"type":"command","command":"x"}}`

Goldens are the expected outputs of `Set(input, "statusLine", value)` with the constant `value = {"type":"command","command":"/opt/gaugewire statusline"}` (compact, exactly these bytes), and of `Delete(input, "statusLine")`:

`empty.set.golden`:
```
{
  "statusLine": {"type":"command","command":"/opt/gaugewire statusline"}
}
```
(no trailing newline after `}`.)

`none.set.golden`:
```json
{
  "model": "claude-sonnet-5",
  "permissions": {
    "allow": ["Bash(git diff *)"]
  },
  "statusLine": {"type":"command","command":"/opt/gaugewire statusline"}
}
```

`only.set.golden`:
```json
{
  "statusLine": {"type":"command","command":"/opt/gaugewire statusline"}
}
```

`first.set.golden`:
```json
{
  "statusLine": {"type":"command","command":"/opt/gaugewire statusline"},
  "model": "claude-sonnet-5"
}
```

`middle.set.golden`:
```json
{
  "model": "claude-sonnet-5",
  "statusLine": {"type":"command","command":"/opt/gaugewire statusline"},
  "cleanupPeriodDays": 20
}
```

`last.set.golden`: as `last.json` with the value replaced: `{\r\n\t"model": "claude-sonnet-5",\r\n\t"statusLine": {"type":"command","command":"/opt/gaugewire statusline"}\r\n}\r\n`

`compact.set.golden`: `{"model":"claude-sonnet-5","statusLine":{"type":"command","command":"/opt/gaugewire statusline"}}`

`empty.delete.golden`: `{}`; `none.delete.golden`: identical to `none.json`; `only.delete.golden`: `{}\n` (removing the only member removes everything between the braces; the newline after `}` survives); `first.delete.golden`:
```json
{
  "model": "claude-sonnet-5"
}
```
`middle.delete.golden`:
```json
{
  "model": "claude-sonnet-5",
  "cleanupPeriodDays": 20
}
```
`last.delete.golden`: `{\r\n\t"model": "claude-sonnet-5"\r\n}\r\n`; `compact.delete.golden`: `{"model":"claude-sonnet-5"}`.

Trailing newlines: every `.json` and `.golden` above ends with a single `\n` (or `\r\n` for the `last` pair) except `empty.json`, `empty.set.golden`, `empty.delete.golden`, `compact.json`, `compact.set.golden` and `compact.delete.golden`, which have none. Write them with the Write tool; the `last` pair must be written with explicit `\r\n`, so create those two with a tiny Go test helper or `printf` and verify with `xxd`.

- [ ] **Step 2: Write the failing tests**

`internal/settings/settings_test.go`:

```go
package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var installed = json.RawMessage(`{"type":"command","command":"/opt/gaugewire statusline"}`)

var fixtures = []string{"empty", "none", "only", "first", "middle", "last", "compact"}

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func TestSetReplacesOrAppendsOnlyTheMember(t *testing.T) {
	t.Parallel()
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := Set(read(t, name+".json"), "statusLine", installed)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			if diff := cmp.Diff(string(read(t, name+".set.golden")), string(got)); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestDeleteRemovesOnlyTheMember(t *testing.T) {
	t.Parallel()
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := Delete(read(t, name+".json"), "statusLine")
			if err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if diff := cmp.Diff(string(read(t, name+".delete.golden")), string(got)); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetThenDeleteRestoresAFileWithoutTheMember(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"empty", "none"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			original := read(t, name+".json")
			set, err := Set(original, "statusLine", installed)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := Delete(set, "statusLine")
			if err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if diff := cmp.Diff(string(original), string(got)); diff != "" {
				t.Fatalf("round trip changed bytes (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSetWithTheOriginalValueRestoresAFileWithTheMember(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"only", "first", "middle", "last", "compact"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			original := read(t, name+".json")
			member, err := Get(original, "statusLine")
			if err != nil || !member.Found {
				t.Fatalf("Get: found=%v err=%v", member.Found, err)
			}
			set, err := Set(original, "statusLine", installed)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := Set(set, "statusLine", member.Value)
			if err != nil {
				t.Fatalf("Set back: %v", err)
			}
			if diff := cmp.Diff(string(original), string(got)); diff != "" {
				t.Fatalf("round trip changed bytes (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetReturnsTheExactValueBytes(t *testing.T) {
	t.Parallel()
	got, err := Get(read(t, "only.json"), "statusLine")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := Member{Found: true, Value: json.RawMessage(`{ "type": "command", "command": "bash ~/.claude/statusline-command.sh", "refreshInterval": 5 }`)}
	if diff := cmp.Diff(want, got, cmpopts.IgnoreUnexported(Member{}), cmp.Comparer(func(a, b json.RawMessage) bool { return string(a) == string(b) })); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestGetOnAMissingMember(t *testing.T) {
	t.Parallel()
	got, err := Get(read(t, "none.json"), "statusLine")
	if err != nil || got.Found || got.Value != nil {
		t.Fatalf("got %+v err %v, want not found", got, err)
	}
}

func TestOperationsRejectANonObject(t *testing.T) {
	t.Parallel()
	_, err := Get([]byte(`["statusLine"]`), "statusLine")
	if !errors.Is(err, ErrNotObject) {
		t.Fatalf("got %v, want ErrNotObject", err)
	}
}

func TestOperationsReportMalformedJSON(t *testing.T) {
	t.Parallel()
	_, err := Set([]byte(`{"statusLine": `), "statusLine", installed)
	if err == nil || errors.Is(err, ErrNotObject) {
		t.Fatalf("got %v, want a decode error", err)
	}
}

func TestLoadTreatsAMissingFileAsAnEmptyObject(t *testing.T) {
	t.Parallel()
	data, existed, err := Load(filepath.Join(t.TempDir(), "settings.json"))
	type outcome struct {
		data    string
		existed bool
		err     bool
	}
	got := outcome{string(data), existed, err != nil}
	want := outcome{"{}", false, false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDefaultPathIsUnderTheUserHome(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no user home")
	}
	got, err := DefaultPath()
	want := filepath.Join(home, ".claude", "settings.json")
	if err != nil || got != want {
		t.Fatalf("got %q err %v, want %q", got, err, want)
	}
}
```

`cmpopts` is `github.com/google/go-cmp/cmp/cmpopts`, part of the go-cmp module already in `go.mod`; add it to the test imports. The offsets stay unexported and untested directly: the goldens pin them.

- [ ] **Step 3: Run the tests and watch them fail**

Run: `go test ./internal/settings/`
Expected: FAIL to build, `undefined: Set`, `undefined: Delete`, `undefined: Get`, `undefined: Member`, `undefined: Load`, `undefined: DefaultPath`, `undefined: ErrNotObject`.

- [ ] **Step 4: Write `internal/settings/settings.go`**

```go
// Package settings edits one top-level member of a JSON object while leaving
// every other byte of the document untouched. Gaugewire uses it to install and
// remove its status-line command in Claude Code's settings file.
package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotObject means the document's top level is not a JSON object.
var ErrNotObject = errors.New("settings: top level is not a JSON object")

// Member is the result of Get: the exact value bytes of a top-level member
// and where they sit in the document.
type Member struct {
	Found bool
	Value json.RawMessage

	keyStart   int // offset of the key's opening quote
	valueStart int // offset of the value's first byte
	valueEnd   int // offset just past the value's last byte
	prevEnd    int // offset just past the previous value, or past "{" for the first member
}

type layout struct {
	afterBrace int    // offset just past "{"
	firstKey   int    // offset of the first key's opening quote, or -1
	lastEnd    int    // offset just past the last value, or afterBrace when empty
	closeBrace int    // offset of the closing "}"
	members    []Member
}

// Get locates key among the top-level members. Value holds the exact source
// bytes of the value, so writing it back reproduces the original document.
func Get(object []byte, key string) (Member, error) {
	l, err := scan(object)
	if err != nil {
		return Member{}, err
	}
	for i, m := range l.members {
		if memberKey(object, m) == key {
			return l.members[i], nil
		}
	}
	return Member{}, nil
}

// Set replaces the member's value in place, or appends the member after the
// last one using the document's own indentation.
func Set(object []byte, key string, value json.RawMessage) ([]byte, error) {
	l, err := scan(object)
	if err != nil {
		return nil, err
	}
	for _, m := range l.members {
		if memberKey(object, m) == key {
			return splice(object, m.valueStart, m.valueEnd, value), nil
		}
	}
	indent := []byte("\n  ")
	if l.firstKey >= 0 {
		indent = object[l.afterBrace:l.firstKey]
	}
	separator := `":"`
	if bytes.ContainsRune(indent, '\n') {
		separator = `": "`
	}
	member := append(append([]byte(`"`+key), separator...), value...)
	if len(l.members) == 0 {
		insert := append(append(append([]byte{}, indent...), member...), '\n')
		return splice(object, l.afterBrace, l.closeBrace, insert), nil
	}
	insert := append(append([]byte{','}, indent...), member...)
	return splice(object, l.lastEnd, l.lastEnd, insert), nil
}

// Delete removes the member, its value and the punctuation that joined it to
// its neighbours. A document without the member is returned unchanged.
func Delete(object []byte, key string) ([]byte, error) {
	l, err := scan(object)
	if err != nil {
		return nil, err
	}
	for i, m := range l.members {
		if memberKey(object, m) != key {
			continue
		}
		switch {
		case len(l.members) == 1:
			return splice(object, l.afterBrace, l.closeBrace, nil), nil
		case i == len(l.members)-1:
			return splice(object, m.prevEnd, m.valueEnd, nil), nil
		default:
			return splice(object, m.keyStart, l.members[i+1].keyStart, nil), nil
		}
	}
	return object, nil
}

// Load reads the settings file. A missing file reads as an empty object.
func Load(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("{}"), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read settings: %w", err)
	}
	return data, true, nil
}

// DefaultPath is Claude Code's user settings file.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func scan(object []byte) (layout, error) {
	dec := json.NewDecoder(bytes.NewReader(object))
	tok, err := dec.Token()
	if err != nil {
		return layout{}, fmt.Errorf("settings: %w", err)
	}
	if tok != json.Delim('{') {
		return layout{}, ErrNotObject
	}
	l := layout{afterBrace: int(dec.InputOffset()), firstKey: -1}
	l.lastEnd = l.afterBrace
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			return layout{}, fmt.Errorf("settings: %w", err)
		}
		keyEnd := int(dec.InputOffset())
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return layout{}, fmt.Errorf("settings: %w", err)
		}
		m := Member{Found: true, Value: raw, prevEnd: l.lastEnd}
		m.keyStart = skip(object, l.lastEnd, " \t\r\n,")
		m.valueStart = skip(object, keyEnd, " \t\r\n:")
		m.valueEnd = m.valueStart + len(raw)
		if l.firstKey < 0 {
			l.firstKey = m.keyStart
		}
		l.lastEnd = m.valueEnd
		l.members = append(l.members, m)
	}
	if _, err := dec.Token(); err != nil {
		return layout{}, fmt.Errorf("settings: %w", err)
	}
	l.closeBrace = int(dec.InputOffset()) - 1
	return l, nil
}

func memberKey(object []byte, m Member) string {
	var key string
	if err := json.Unmarshal(object[m.keyStart:keyEndOf(object, m)], &key); err != nil {
		return ""
	}
	return key
}

// keyEndOf finds the offset just past the key string that starts at keyStart,
// honouring backslash escapes.
func keyEndOf(object []byte, m Member) int {
	for i := m.keyStart + 1; i < len(object); i++ {
		switch object[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(object)
}

func skip(object []byte, from int, set string) int {
	for from < len(object) && bytes.IndexByte([]byte(set), object[from]) >= 0 {
		from++
	}
	return from
}

func splice(object []byte, start, end int, replacement []byte) []byte {
	out := make([]byte, 0, len(object)-(end-start)+len(replacement))
	out = append(out, object[:start]...)
	out = append(out, replacement...)
	return append(out, object[end:]...)
}
```

Two facts this code relies on, to verify with the fixtures rather than assume: after `dec.Token()` returns a key string, `dec.InputOffset()` is just past its closing quote; `dec.Decode(&json.RawMessage)` stores the exact source bytes of the value without leading or trailing whitespace. If either fails against a golden, fix the offset arithmetic, never the golden. Duplicate keys: the first occurrence wins, which matches `encoding/json`'s last-wins only in the trivial case; the settings file never has duplicates and the spec does not promise anything about them.

- [ ] **Step 5: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/settings/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 6: Commit**

```bash
git add internal/settings
git commit -m "$(cat <<'EOF'
feat: splice one member of a settings file without touching the rest

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Dead-letter reader and the exported version gate

**Files:**
- Create: `internal/store/deadletter.go`, `internal/store/deadletter_test.go`
- Modify: `internal/source/claude/parse.go`, `internal/source/claude/parse_test.go`

**Interfaces:**
- Produces: `store.DeadLetterEntry{EventID string; Reason string; DeadLetteredAt time.Time}`; `store.ListDeadLetters(home string) (entries []DeadLetterEntry, unreadable int, err error)` (newest first by `DeadLetteredAt`, then by event id; `.unreadable` files counted, never decoded); `claude.VersionSupported(version string) bool` (true when `version` parses as `major.minor.patch` with optional suffix and is at least `MinimumVersion`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/deadletter_test.go` (new file, package `store`; `spoolHome` and `event` helpers live in `spool_test.go`):

```go
package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestListDeadLettersNewestFirstAndCountsUnreadable(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	for _, id := range []string{"evt-a", "evt-b"} {
		if _, err := WritePending(home, event(id, captured)); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
		captured = captured.Add(time.Minute)
	}
	pending, err := ListPending(home)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := DeadLetter(home, pending[0], "databox-main: permanent (invalid_api_key): 401", captured.Add(time.Hour)); err != nil {
		t.Fatalf("dead-letter a: %v", err)
	}
	if err := DeadLetter(home, pending[1], "databox-main: permanent (forbidden): 403", captured.Add(2*time.Hour)); err != nil {
		t.Fatalf("dead-letter b: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, DeadLetterDir, "1789657000000-evt-bad.json.unreadable"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write unreadable: %v", err)
	}
	entries, unreadable, err := ListDeadLetters(home)
	type outcome struct {
		entries    []DeadLetterEntry
		unreadable int
		err        bool
	}
	got := outcome{entries, unreadable, err != nil}
	want := outcome{
		entries: []DeadLetterEntry{
			{EventID: "evt-b", Reason: "databox-main: permanent (forbidden): 403", DeadLetteredAt: captured.Add(2 * time.Hour)},
			{EventID: "evt-a", Reason: "databox-main: permanent (invalid_api_key): 401", DeadLetteredAt: captured.Add(time.Hour)},
		},
		unreadable: 1,
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(outcome{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestListDeadLettersOnAnEmptyHome(t *testing.T) {
	t.Parallel()
	entries, unreadable, err := ListDeadLetters(t.TempDir())
	if err != nil || unreadable != 0 || len(entries) != 0 {
		t.Fatalf("got %v %d %v, want nothing", entries, unreadable, err)
	}
}
```

Read `DeadLetter` in `spool.go` first: it records `DeadLetteredAt` and `Reason` on the event before writing; the expected values above follow from that.

Append to `internal/source/claude/parse_test.go`:

```go
func TestVersionSupported(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{"2.1.251": true, "2.1.274": true, "3.0.0": true, "2.1.250": false, "2.0.999": false, "": false, "latest": false, "2.1.251-beta": true}
	got := map[string]bool{}
	for v := range cases {
		got[v] = VersionSupported(v)
	}
	if diff := cmp.Diff(cases, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}
```

Check how `parseVersion` treats a suffix like `-beta` before trusting that case; if the existing parser rejects suffixes, keep its behaviour and set that expectation to `false`, noting it in the report.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/store/ ./internal/source/claude/`
Expected: FAIL to build, `undefined: ListDeadLetters`, `undefined: DeadLetterEntry`, `undefined: VersionSupported`.

- [ ] **Step 3: Write the implementation**

`internal/store/deadletter.go`:

```go
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DeadLetterEntry summarises one dead-lettered event for status and doctor.
type DeadLetterEntry struct {
	EventID        string
	Reason         string
	DeadLetteredAt time.Time
}

// ListDeadLetters reads dead-letter/ newest first. Files quarantined as
// .unreadable are counted, never decoded, since their content is by definition
// not an event.
func ListDeadLetters(home string) ([]DeadLetterEntry, int, error) {
	dir := filepath.Join(home, DeadLetterDir)
	dirEntries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("read %s: %w", dir, err)
	}
	var entries []DeadLetterEntry
	unreadable := 0
	for _, de := range dirEntries {
		name := de.Name()
		switch {
		case strings.HasSuffix(name, ".unreadable"):
			unreadable++
		case strings.HasSuffix(name, ".json"):
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, 0, fmt.Errorf("read %s: %w", name, err)
			}
			var ev Event
			if err := json.Unmarshal(raw, &ev); err != nil {
				return nil, 0, fmt.Errorf("decode %s: %w", name, err)
			}
			entry := DeadLetterEntry{EventID: ev.Snapshot.EventID, Reason: ev.Reason}
			if ev.DeadLetteredAt != nil {
				entry.DeadLetteredAt = *ev.DeadLetteredAt
			}
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if !entries[i].DeadLetteredAt.Equal(entries[j].DeadLetteredAt) {
			return entries[i].DeadLetteredAt.After(entries[j].DeadLetteredAt)
		}
		return entries[i].EventID < entries[j].EventID
	})
	return entries, unreadable, nil
}
```

Use `errors.Is(err, os.ErrNotExist)` if the linter prefers it over `os.IsNotExist`. `slices.SortFunc` is equally fine.

In `internal/source/claude/parse.go` add, next to `MinimumVersion`:

```go
// VersionSupported reports whether a Claude Code version string is at least
// MinimumVersion, the first release whose status-line payload carries rate limits.
func VersionSupported(version string) bool {
	return versionAtLeast(version, MinimumVersion)
}
```

and, if `Parse` has its own inline gate, make it call `VersionSupported` so there is one gate.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/store/ ./internal/source/claude/` then `make check`.
Expected: `ok`, lint clean; the coverage gate for `internal/source/claude` must stay ≥ 90 % (`make cover`).

- [ ] **Step 5: Commit**

```bash
git add internal/store internal/source/claude
git commit -m "$(cat <<'EOF'
feat: list dead letters newest first and export the version gate

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `gaugewire install`

**Files:**
- Create: `internal/cli/install.go`, `internal/cli/install_test.go`
- Modify: `internal/cli/cli.go` (case + usage line); `internal/config/config.go` (one tag: `OriginalStatusLine json.RawMessage \`json:"originalStatusLine,omitempty"\``, so an install over a file with no `statusLine` saves no member instead of `null`, and `Load` gives back a nil value that uninstall recognises as "there was none")

**Interfaces:**
- Consumes: `settings.Load/Get/Set/DefaultPath`, `config.Load/Save/Default/ErrMissing/ErrInvalid/Install/Identity/Renderer`, `store.Home/EnsureLayout/WriteFileAtomic`, `uuid.NewV4`, `os.Executable`, `os.Hostname`.
- Produces: unexported `runInstall(ctx context.Context, args []string, info BuildInfo, streams IO) error`; `installOptions{settingsPath, nodeAlias, accountAlias string; force bool; executable string; now func() time.Time; hostname func() (string, error)}`; `install(home string, opts installOptions, stdout io.Writer) error`; `installedCommand(executable string) string` (forward slashes; the path is wrapped in double quotes only when it contains whitespace); `ErrAlreadyInstalled` (sentinel: "status line already points at gaugewire; pass --force to reinstall").

Output of a successful install, one line per fact, exactly:

```
settings:     <settingsPath>
backup:       <backup path, or "none (file did not exist)">
status line:  <installedCommand>
renderer:     <original command, or "none">
node:         <alias> <id>
account:      <alias> <id>
```

- [ ] **Step 1: Write the failing tests**

`internal/cli/install_test.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/settings"
)

var installAt = time.Date(2026, 9, 18, 10, 30, 0, 0, time.UTC)

func installOpts(settingsPath string) installOptions {
	return installOptions{
		settingsPath: settingsPath,
		executable:   "/opt/gaugewire/bin/gaugewire",
		now:          func() time.Time { return installAt },
		hostname:     func() (string, error) { return "mac-mini-01", nil },
	}
}

type installed struct {
	err      string
	settings string
	backup   string
	cfg      config.Config
	stdout   string
}

func snapshotInstall(t *testing.T, home, settingsPath string, err error, stdout string) installed {
	t.Helper()
	out := installed{stdout: stdout}
	if err != nil {
		out.err = err.Error()
	}
	if data, readErr := os.ReadFile(settingsPath); readErr == nil {
		out.settings = string(data)
	}
	if data, readErr := os.ReadFile(settingsPath + ".gaugewire-backup-20260918-103000"); readErr == nil {
		out.backup = string(data)
	}
	if cfg, loadErr := config.Load(home); loadErr == nil {
		cfg.Node.ID, cfg.Account.ID = "", "" // generated; asserted separately
		out.cfg = cfg
	}
	return out
}

func TestInstallSplicesTheStatusLineAndSavesTheOriginal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	original := "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": { \"type\": \"command\", \"command\": \"bash ~/.claude/statusline-command.sh\", \"refreshInterval\": 5 }\n}\n"
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	var stdout bytes.Buffer
	err := install(home, installOpts(settingsPath), &stdout)
	got := snapshotInstall(t, home, settingsPath, err, stdout.String())
	cfg := config.Default()
	cfg.Node = config.Identity{Alias: "mac-mini-01"}
	cfg.Account = config.Identity{Alias: "claude-01"}
	cfg.Renderer = config.Renderer{Command: "bash ~/.claude/statusline-command.sh"}
	cfg.Install = &config.Install{
		SettingsPath:       settingsPath,
		InstalledCommand:   "/opt/gaugewire/bin/gaugewire statusline",
		OriginalStatusLine: json.RawMessage(`{ "type": "command", "command": "bash ~/.claude/statusline-command.sh", "refreshInterval": 5 }`),
	}
	want := installed{
		settings: "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": { \"type\": \"command\", \"command\": \"/opt/gaugewire/bin/gaugewire statusline\", \"refreshInterval\": 5 }\n}\n",
		backup:   original,
		cfg:      cfg,
		stdout: strings.Join([]string{
			"settings:     " + settingsPath,
			"backup:       " + settingsPath + ".gaugewire-backup-20260918-103000",
			"status line:  /opt/gaugewire/bin/gaugewire statusline",
			"renderer:     bash ~/.claude/statusline-command.sh",
		}, "\n") + "\n",
	}
	// node and account lines carry generated ids; compare them by prefix below.
	gotLines := strings.Split(strings.TrimSpace(got.stdout), "\n")
	got.stdout = strings.Join(gotLines[:4], "\n") + "\n"
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(installed{}), cmp.Transformer("raw", func(r json.RawMessage) string { return string(r) })); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
	if len(gotLines) != 6 || !strings.HasPrefix(gotLines[4], "node:         mac-mini-01 ") || !strings.HasPrefix(gotLines[5], "account:      claude-01 ") {
		t.Fatalf("identity lines: %q", gotLines[4:])
	}
}

func TestInstallCreatesTheSettingsFileWhenMissing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), ".claude", "settings.json")
	var stdout bytes.Buffer
	err := install(home, installOpts(settingsPath), &stdout)
	got := snapshotInstall(t, home, settingsPath, err, stdout.String())
	cfg := config.Default()
	cfg.Node = config.Identity{Alias: "mac-mini-01"}
	cfg.Account = config.Identity{Alias: "claude-01"}
	cfg.Install = &config.Install{SettingsPath: settingsPath, InstalledCommand: "/opt/gaugewire/bin/gaugewire statusline", OriginalStatusLine: nil}
	want := installed{
		settings: "{\n  \"statusLine\": {\"type\":\"command\",\"command\":\"/opt/gaugewire/bin/gaugewire statusline\"}\n}",
		cfg:      cfg,
	}
	got.stdout = ""
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(installed{}), cmp.Transformer("raw", func(r json.RawMessage) string { return string(r) })); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestInstallRefusesWhenAlreadyInstalled(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	before, _ := os.ReadFile(settingsPath)
	err := install(home, installOpts(settingsPath), &bytes.Buffer{})
	after, _ := os.ReadFile(settingsPath)
	if !errors.Is(err, ErrAlreadyInstalled) || string(before) != string(after) {
		t.Fatalf("err=%v changed=%v, want ErrAlreadyInstalled and no change", err, string(before) != string(after))
	}
}

func TestInstallWithForceKeepsTheSavedOriginal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"cat"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	opts := installOpts(settingsPath)
	opts.force = true
	opts.executable = "/usr/local/bin/gaugewire"
	err := install(home, opts, &bytes.Buffer{})
	cfg, loadErr := config.Load(home)
	data, _ := os.ReadFile(settingsPath)
	type outcome struct {
		err      bool
		loadErr  bool
		renderer string
		original string
		command  string
		file     string
	}
	got := outcome{err != nil, loadErr != nil, cfg.Renderer.Command, string(cfg.Install.OriginalStatusLine), cfg.Install.InstalledCommand, string(data)}
	want := outcome{false, false, "cat", `{"type":"command","command":"cat"}`, "/usr/local/bin/gaugewire statusline", `{"statusLine":{"type":"command","command":"/usr/local/bin/gaugewire statusline"}}`}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInstallKeepsExistingIdentityAndAliasesOverride(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	existing := testConfig()
	if err := config.Save(home, existing); err != nil {
		t.Fatalf("save: %v", err)
	}
	opts := installOpts(settingsPath)
	opts.nodeAlias = "studio-02"
	if err := install(home, opts, &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	cfg, err := config.Load(home)
	type identity struct{ nodeID, nodeAlias, accountID, accountAlias string }
	got := identity{cfg.Node.ID, cfg.Node.Alias, cfg.Account.ID, cfg.Account.Alias}
	want := identity{existing.Node.ID, "studio-02", existing.Account.ID, existing.Account.Alias}
	if err != nil || got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

func TestInstalledCommandQuotesOnlyPathsWithSpaces(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/usr/local/bin/gaugewire":                  "/usr/local/bin/gaugewire statusline",
		`C:\Program Files\Gaugewire\gaugewire.exe`:  `"C:/Program Files/Gaugewire/gaugewire.exe" statusline`,
		`C:\Users\me\gaugewire.exe`:                 "C:/Users/me/gaugewire.exe statusline",
	}
	got := map[string]string{}
	for exe := range cases {
		got[exe] = installedCommand(exe)
	}
	if diff := cmp.Diff(cases, got); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestRunInstallUsesTheFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	var stdout bytes.Buffer
	err := runInstall(t.Context(), []string{"--settings", settingsPath, "--node-alias", "n1", "--account-alias", "a1"}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	cfg, loadErr := config.Load(home)
	type outcome struct {
		err, loadErr       bool
		nodeAlias, account string
		settingsPath       string
	}
	got := outcome{err != nil, loadErr != nil, cfg.Node.Alias, cfg.Account.Alias, cfg.Install.SettingsPath}
	want := outcome{false, false, "n1", "a1", settingsPath}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

`installedCommand` on a Windows path uses `filepath.ToSlash`, which only converts on Windows; write it with `strings.ReplaceAll(exe, "\\", "/")` so the test holds on every platform, and say so in the doc comment (the spec asks for forward slashes because Git Bash strips unquoted backslashes; the public status line page states this). Add `"github.com/sulcer/gaugewire/internal/store"` to the test imports for `store.HomeEnv`.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL to build, `undefined: install`, `undefined: installOptions`, `undefined: runInstall`, `undefined: installedCommand`, `undefined: ErrAlreadyInstalled`.

- [ ] **Step 3: Write `internal/cli/install.go`**

```go
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
	"strings"
	"time"
	"uuid"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/settings"
	"github.com/sulcer/gaugewire/internal/store"
)

// ErrAlreadyInstalled means the status line already runs gaugewire.
var ErrAlreadyInstalled = errors.New("status line already points at gaugewire; pass --force to reinstall")

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
// the settings file and records what it changed.
func install(home string, opts installOptions, stdout io.Writer) error {
	if err := store.EnsureLayout(home); err != nil {
		return err
	}
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
	alreadyOurs := isGaugewire(currentCommand)
	if alreadyOurs && !opts.force {
		return ErrAlreadyInstalled
	}
	if !alreadyOurs {
		cfg.Renderer.Command = currentCommand
		cfg.Install = &config.Install{OriginalStatusLine: current.Value}
	}
	if cfg.Install == nil {
		cfg.Install = &config.Install{}
	}
	cfg.Install.SettingsPath = opts.settingsPath
	cfg.Install.InstalledCommand = command
	newValue, err := statusLineWith(current.Value, command)
	if err != nil {
		return err
	}
	updated, err := settings.Set(file, "statusLine", newValue)
	if err != nil {
		return err
	}
	backup := "none (file did not exist)"
	mode := os.FileMode(0o600)
	if existed {
		if info, statErr := os.Stat(opts.settingsPath); statErr == nil {
			mode = info.Mode().Perm()
		}
		backup = opts.settingsPath + ".gaugewire-backup-" + opts.now().UTC().Format("20060102-150405")
		if err := store.WriteFileAtomic(backup, file, 0o600); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
	} else if err := os.MkdirAll(filepath.Dir(opts.settingsPath), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	if err := store.WriteFileAtomic(opts.settingsPath, updated, mode); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if err := config.Save(home, cfg); err != nil {
		return err
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
// forward slashes because Git Bash strips unquoted backslashes; the path is
// quoted only when it contains whitespace.
func installedCommand(executable string) string {
	path := strings.ReplaceAll(executable, `\`, "/")
	if strings.ContainsAny(path, " \t") {
		path = `"` + path + `"`
	}
	return path + " statusline"
}

func isGaugewire(command string) bool {
	trimmed := strings.TrimSpace(command)
	return strings.HasSuffix(trimmed, "gaugewire statusline") || strings.HasSuffix(trimmed, "gaugewire.exe statusline") || strings.HasSuffix(trimmed, `gaugewire" statusline`) || strings.HasSuffix(trimmed, `gaugewire.exe" statusline`)
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

// statusLineWith returns the statusLine object with its command replaced, or
// a new {"type":"command","command":...} object when there was none.
func statusLineWith(value json.RawMessage, command string) (json.RawMessage, error) {
	quoted, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	if len(value) == 0 {
		return json.RawMessage(`{"type":"command","command":` + string(quoted) + `}`), nil
	}
	updated, err := settings.Set(value, "command", quoted)
	if err != nil {
		return nil, fmt.Errorf("statusLine is not an object: %w", err)
	}
	return updated, nil
}
```

Add to `cli.go`: `case "install": return runInstall(ctx, args[1:], info, streams)` and the usage line `  install [--settings path] [--node-alias a] [--account-alias b] [--force]\n                      install the status-line adapter`. Keep the usage text's existing lines and order: statusline, flush, install, uninstall (Task 4), status, doctor (Task 5), version.

The renderer command when the original `statusLine` had no `command` member (or was not an object) is `""`; `statusLineWith` then fails with a clear error on a non-object, which is the right refusal: install must not guess.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/cli/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "$(cat <<'EOF'
feat: install the status-line adapter by splicing settings.json

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `gaugewire uninstall [--purge]`

**Files:**
- Create: `internal/cli/uninstall.go`, `internal/cli/uninstall_test.go`
- Modify: `internal/cli/cli.go`

**Interfaces:**
- Produces: `runUninstall(ctx, args, info, streams) error`; `uninstall(home, settingsPath string, purge bool, stdout io.Writer) error` (`settingsPath` empty means "the path recorded at install"); `ErrNotInstalled` ("gaugewire is not installed on this machine"); prints `restored: <path>` or `removed statusLine from <path>`, then `purged: <home>` when `--purge`; when the current command is not ours prints `warning: statusLine was changed since install; nothing restored` and returns nil without touching the file, still clearing nothing.

- [ ] **Step 1: Write the failing tests**

`internal/cli/uninstall_test.go`:

```go
package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sulcer/gaugewire/internal/config"
)

func installFixture(t *testing.T, original string) (home, settingsPath string) {
	t.Helper()
	home = t.TempDir()
	settingsPath = filepath.Join(t.TempDir(), "settings.json")
	if original != "" {
		if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	return home, settingsPath
}

type uninstalled struct {
	err       bool
	settings  string
	installed bool
	stdout    string
	homeGone  bool
}

func snapshotUninstall(t *testing.T, home, settingsPath string, err error, stdout string) uninstalled {
	t.Helper()
	out := uninstalled{err: err != nil, stdout: stdout}
	if data, readErr := os.ReadFile(settingsPath); readErr == nil {
		out.settings = string(data)
	}
	if cfg, loadErr := config.Load(home); loadErr == nil {
		out.installed = cfg.Install != nil
	}
	_, statErr := os.Stat(home)
	out.homeGone = os.IsNotExist(statErr)
	return out
}

func TestUninstallRestoresTheOriginalBytes(t *testing.T) {
	t.Parallel()
	original := "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": { \"type\": \"command\", \"command\": \"bash ~/.claude/statusline-command.sh\", \"refreshInterval\": 5 }\n}\n"
	home, settingsPath := installFixture(t, original)
	var stdout bytes.Buffer
	err := uninstall(home, "", false, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: original, installed: false, stdout: "restored: " + settingsPath + "\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUninstallRemovesTheMemberItAdded(t *testing.T) {
	t.Parallel()
	original := "{\n  \"model\": \"claude-sonnet-5\"\n}\n"
	home, settingsPath := installFixture(t, original)
	var stdout bytes.Buffer
	err := uninstall(home, "", false, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: original, installed: false, stdout: "removed statusLine from " + settingsPath + "\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUninstallLeavesAChangedStatusLineAlone(t *testing.T) {
	t.Parallel()
	home, settingsPath := installFixture(t, `{"statusLine":{"type":"command","command":"cat"}}`)
	changed := `{"statusLine":{"type":"command","command":"jq -r .model.display_name"}}`
	if err := os.WriteFile(settingsPath, []byte(changed), 0o600); err != nil {
		t.Fatalf("change: %v", err)
	}
	var stdout bytes.Buffer
	err := uninstall(home, "", false, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: changed, installed: true, stdout: "warning: statusLine was changed since install; nothing restored\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUninstallWithPurgeDeletesTheHome(t *testing.T) {
	t.Parallel()
	original := `{"statusLine":{"type":"command","command":"cat"}}`
	home, settingsPath := installFixture(t, original)
	var stdout bytes.Buffer
	err := uninstall(home, "", true, &stdout)
	got := snapshotUninstall(t, home, settingsPath, err, stdout.String())
	want := uninstalled{settings: original, installed: false, homeGone: true, stdout: "restored: " + settingsPath + "\npurged: " + home + "\n"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUninstallWithoutAnInstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := config.Save(home, testConfig()); err != nil {
		t.Fatalf("save: %v", err)
	}
	err := uninstall(home, "", false, &bytes.Buffer{})
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("got %v, want ErrNotInstalled", err)
	}
}
```

`installed` in `TestUninstallWithPurgeDeletesTheHome` is false because `config.Load` fails on the purged home; the composite value captures that.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL to build, `undefined: uninstall`, `undefined: ErrNotInstalled`.

- [ ] **Step 3: Write `internal/cli/uninstall.go`**

```go
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/settings"
	"github.com/sulcer/gaugewire/internal/store"
)

// ErrNotInstalled means config.json records no install to undo.
var ErrNotInstalled = errors.New("gaugewire is not installed on this machine")

func runUninstall(_ context.Context, args []string, _ BuildInfo, streams IO) error {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(streams.Stderr)
	purge := flags.Bool("purge", false, "also delete the home directory")
	settingsPath := flags.String("settings", "", "settings file to restore (default: the one recorded at install)")
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
	return uninstall(home, *settingsPath, *purge, streams.Stdout)
}

// uninstall restores the status line recorded at install when it is still
// ours, clears the install record, and optionally deletes the home directory.
func uninstall(home, settingsPath string, purge bool, stdout io.Writer) error {
	cfg, err := config.Load(home)
	if errors.Is(err, config.ErrMissing) {
		return ErrNotInstalled
	}
	if err != nil && !errors.Is(err, config.ErrInvalid) {
		return err
	}
	if cfg.Install == nil {
		return ErrNotInstalled
	}
	if settingsPath == "" {
		settingsPath = cfg.Install.SettingsPath
	}
	file, _, err := settings.Load(settingsPath)
	if err != nil {
		return err
	}
	current, err := settings.Get(file, "statusLine")
	if err != nil {
		return err
	}
	if commandOf(current.Value) != cfg.Install.InstalledCommand {
		fmt.Fprintln(stdout, "warning: statusLine was changed since install; nothing restored")
		return nil
	}
	var restored []byte
	hadNone := len(cfg.Install.OriginalStatusLine) == 0 || string(cfg.Install.OriginalStatusLine) == "null"
	if hadNone {
		restored, err = settings.Delete(file, "statusLine")
	} else {
		restored, err = settings.Set(file, "statusLine", cfg.Install.OriginalStatusLine)
	}
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(settingsPath); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := store.WriteFileAtomic(settingsPath, restored, mode); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	if hadNone {
		fmt.Fprintf(stdout, "removed statusLine from %s\n", settingsPath)
	} else {
		fmt.Fprintf(stdout, "restored: %s\n", settingsPath)
	}
	cfg.Install = nil
	if purge {
		if err := os.RemoveAll(home); err != nil {
			return fmt.Errorf("purge home: %w", err)
		}
		fmt.Fprintf(stdout, "purged: %s\n", home)
		return nil
	}
	return config.Save(home, cfg)
}
```

Add `case "uninstall": return runUninstall(ctx, args[1:], info, streams)` and the usage line `  uninstall [--purge]  restore the previous status line` to `cli.go`.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/cli/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "$(cat <<'EOF'
feat: uninstall restores the original status line byte for byte

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: `gaugewire doctor`

**Files:**
- Create: `internal/cli/doctor.go`, `internal/cli/doctor_checks.go`, `internal/cli/doctor_test.go`, `internal/cli/testdata/doctor_healthy.golden`, `internal/cli/testdata/doctor_unhealthy.golden`
- Modify: `internal/cli/cli.go`, `internal/cli/status.go`, `internal/cli/status_test.go`, `internal/cli/testdata/status_deadletters.golden`

**Interfaces:**
- Produces: `ErrUnhealthy` ("one or more checks failed"); `check{name, detail string; ok bool}`; `doctorInput{home, settingsPath, workDir string; now time.Time; runRenderer func(ctx, command string) error}`; `runDoctor(ctx, args, info, streams) error` (flag `--settings`); `diagnose(ctx, in doctorInput) []check`; `renderDoctor(checks []check) string`; per-row functions in `doctor_checks.go`: `checkConfiguration`, `checkClaudeCodeVersion`, `checkStatusLineIntegration`, `checkOverrides`, `checkRenderer`, `checkIdentity`, `checkHomeDirectory`, `checkQuotaWindows`, `checkRefreshInterval`, `checkSpool`. Also `status` gains the newest dead-letter reason on its "Dead letters" line.

Output: one line per check, `✓ <name>: <detail>` or `✗ <name>: <detail>`, then a blank line and `HEALTHY` or `UNHEALTHY`. Rows and their pass rules:

| Row | ✓ when | detail |
|---|---|---|
| configuration | `config.Load` succeeds | `valid` / the error |
| claude code version | no observation yet, or `claude.VersionSupported(state.ClaudeCodeVersion)` | `no observation yet` / `<v> (minimum <MinimumVersion>)` |
| status-line integration | settings file's `statusLine.command` equals `cfg.Install.InstalledCommand` | `<command>` / `not installed` / `points elsewhere: <command>` |
| overrides | neither `<workDir>/.claude/settings.json` nor `<workDir>/.claude/settings.local.json` has `statusLine`, and none of the three files has `"disableAllHooks": true` | `none in <workDir>` / `<file> overrides statusLine` / `<file> sets disableAllHooks` (the settings reference states `disableAllHooks` outside managed settings disables the status line) |
| renderer | `cfg.Renderer.Command` is empty, or `runRenderer` returns nil | `none configured` / `<command> ran` / `<command>: <error>` |
| identity | both ids parse as UUIDs and both aliases are set (already guaranteed by `Validate`; shown for the reader) | `<nodeAlias> / <accountAlias>` |
| home directory | home exists, state loads without error, `pending/` accepts a temp file | `<home>` / the failing step |
| quota windows | always ✓ | `5h <status>, 7d <status>, last observation <ago or never>` |
| refreshInterval | always ✓ | `not set` / `<n>s; each tick runs gaugewire` |
| spool | always ✓ when countable | `<pending> pending, <dead> dead-lettered` plus `, newest: <reason>` when dead > 0, plus `, <n> unreadable` when n > 0 |

Not built in this plan and not printed: sink auth, datasets, last ingestion (they arrive with the Databox sink).

- [ ] **Step 1: Write the goldens and the failing tests**

`internal/cli/testdata/doctor_healthy.golden`:

```
✓ configuration: valid
✓ claude code version: 2.1.274 (minimum 2.1.251)
✓ status-line integration: /opt/gaugewire/bin/gaugewire statusline
✓ overrides: none in <workDir>
✓ renderer: cat ran
✓ identity: mac-mini-01 / claude-01
✓ home directory: <home>
✓ quota windows: 5h observed, 7d observed, last observation 2m ago
✓ refreshInterval: not set
✓ spool: 0 pending, 0 dead-lettered

HEALTHY
```

`internal/cli/testdata/doctor_unhealthy.golden`:

```
✓ configuration: valid
✗ claude code version: 2.1.100 (minimum 2.1.251)
✗ status-line integration: points elsewhere: jq -r .model.display_name
✗ overrides: <workDir>/.claude/settings.local.json overrides statusLine
✗ renderer: exit 3: renderer: exit status 3
✓ identity: mac-mini-01 / claude-01
✓ home directory: <home>
✓ quota windows: 5h unknown, 7d unknown, last observation 5h ago
✓ refreshInterval: 5s; each tick runs gaugewire
✓ spool: 1 pending, 1 dead-lettered, newest: databox-main: permanent (invalid_api_key): 401, 1 unreadable

UNHEALTHY
```

The tests substitute `<home>` and `<workDir>` with the temp paths before comparing (`strings.ReplaceAll` on the golden), so the goldens stay stable. Both files end with a single trailing newline.

`internal/cli/doctor_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sulcer/gaugewire/internal/config"
	"github.com/sulcer/gaugewire/internal/quota"
	"github.com/sulcer/gaugewire/internal/store"
)

var doctorNow = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func doctorGolden(t *testing.T, name, home, workDir string) string {
	t.Helper()
	g := golden(t, name)
	return strings.ReplaceAll(strings.ReplaceAll(g, "<home>", home), "<workDir>", workDir)
}

func TestDoctorHealthy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	workDir := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"cat"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	state := store.NewState()
	observed := doctorNow.Add(-2 * time.Minute)
	used := 24.0
	reset := doctorNow.Add(time.Hour)
	state.Windows = quota.Windows{
		FiveHour: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used, ResetsAt: &reset},
		SevenDay: quota.Window{Status: quota.WindowObserved, UsedPercentage: &used, ResetsAt: &reset},
	}
	state.LastObservedAt = &observed
	state.ClaudeCodeVersion = "2.1.274"
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	in := doctorInput{home: home, settingsPath: settingsPath, workDir: workDir, now: doctorNow, runRenderer: func(context.Context, string) error { return nil }}
	got := renderDoctor(diagnose(t.Context(), in))
	if want := doctorGolden(t, "doctor_healthy.golden", home, workDir); got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDoctorUnhealthy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	workDir := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"cat","refreshInterval":5}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"jq -r .model.display_name","refreshInterval":5}}`), 0o600); err != nil {
		t.Fatalf("change: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workDir, ".claude"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".claude", "settings.local.json"), []byte(`{"statusLine":{"type":"command","command":"echo local"}}`), 0o600); err != nil {
		t.Fatalf("local: %v", err)
	}
	state := store.NewState()
	observed := doctorNow.Add(-5 * time.Hour)
	state.LastObservedAt = &observed
	state.ClaudeCodeVersion = "2.1.100"
	if err := store.SaveState(home, state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	at := doctorNow.Add(-time.Hour)
	for _, id := range []string{"evt-1", "evt-2"} {
		ev := store.Event{EventType: quota.EventChange, Snapshot: quota.Snapshot{SchemaVersion: 1, EventID: id, CapturedAt: at}, Delivery: map[string]store.DeliveryState{"databox-main": {NextAttemptAt: at}}}
		if _, err := store.WritePending(home, ev); err != nil {
			t.Fatalf("spool: %v", err)
		}
		at = at.Add(time.Minute)
	}
	pending, _ := store.ListPending(home)
	if err := store.DeadLetter(home, pending[0], "databox-main: permanent (invalid_api_key): 401", doctorNow.Add(-30*time.Minute)); err != nil {
		t.Fatalf("dead-letter: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, store.DeadLetterDir, "1789657000000-evt-bad.json.unreadable"), []byte("{"), 0o600); err != nil {
		t.Fatalf("unreadable: %v", err)
	}
	in := doctorInput{home: home, settingsPath: settingsPath, workDir: workDir, now: doctorNow, runRenderer: func(context.Context, string) error { return errors.New("exit 3: renderer: exit status 3") }}
	got := renderDoctor(diagnose(t.Context(), in))
	if want := doctorGolden(t, "doctor_unhealthy.golden", home, workDir); got != want {
		t.Fatalf("doctor mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunDoctorExitsUnhealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := config.Save(home, testConfig()); err != nil {
		t.Fatalf("save: %v", err)
	}
	var stdout bytes.Buffer
	err := runDoctor(t.Context(), []string{"--settings", settingsPath}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if !errors.Is(err, ErrUnhealthy) || !strings.HasSuffix(stdout.String(), "\nUNHEALTHY\n") || !strings.Contains(stdout.String(), "✗ status-line integration: not installed") {
		t.Fatalf("err=%v stdout=%q", err, stdout.String())
	}
}

func TestRunDoctorHealthyReturnsNil(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	home := t.TempDir()
	t.Setenv(store.HomeEnv, home)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"statusLine":{"type":"command","command":"cat"}}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := install(home, installOpts(settingsPath), &bytes.Buffer{}); err != nil {
		t.Fatalf("install: %v", err)
	}
	var stdout bytes.Buffer
	err := runDoctor(t.Context(), []string{"--settings", settingsPath}, BuildInfo{}, IO{Stdout: &stdout, Stderr: &bytes.Buffer{}})
	if err != nil || !strings.HasSuffix(stdout.String(), "\nHEALTHY\n") {
		t.Fatalf("err=%v stdout=%q", err, stdout.String())
	}
}
```

The last test runs the real renderer (`cat`) with the embedded sample payload; the integration row compares the settings file with the command recorded in `config.json`, so the executable path used at install does not need to exist.

`status` addition: `internal/cli/testdata/status_deadletters.golden` is `status_fresh.golden` with the `Dead letters` line replaced by `Dead letters:      1 · newest: databox-main: permanent (invalid_api_key): 401`. Add `TestRenderStatusShowsTheNewestDeadLetterReason` to `status_test.go`: `renderStatus` gains a parameter `newest string` (empty hides the suffix); call it with `dead = 1` and the reason above and compare with the golden; update the two existing calls to pass `""`.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/cli/`
Expected: FAIL to build, `undefined: doctorInput`, `undefined: diagnose`, `undefined: renderDoctor`, `undefined: runDoctor`, `undefined: ErrUnhealthy`, wrong argument count for `renderStatus`.

- [ ] **Step 3: Write the implementation**

`internal/cli/doctor.go`:

```go
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
```

`internal/cli/doctor_checks.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"errors"
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

func loadConfigForDoctor(home string) (config.Config, error) {
	cfg, err := config.Load(home)
	if err != nil && !errors.Is(err, config.ErrInvalid) {
		return config.Config{}, err
	}
	return cfg, err
}

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
// disable hooks, both of which stop the installed command from running.
func checkOverrides(in doctorInput) check {
	name := "overrides"
	project := []string{filepath.Join(in.workDir, ".claude", "settings.json"), filepath.Join(in.workDir, ".claude", "settings.local.json")}
	for _, path := range project {
		file, existed, err := settings.Load(path)
		if err != nil || !existed {
			continue
		}
		if m, err := settings.Get(file, "statusLine"); err == nil && m.Found {
			return check{name: name, detail: path + " overrides statusLine"}
		}
	}
	for _, path := range append(project, in.settingsPath) {
		file, existed, err := settings.Load(path)
		if err != nil || !existed {
			continue
		}
		if m, err := settings.Get(file, "disableAllHooks"); err == nil && m.Found && string(m.Value) == "true" {
			return check{name: name, detail: path + " sets disableAllHooks"}
		}
	}
	return check{name: name, ok: true, detail: "none in " + in.workDir}
}

func checkRenderer(ctx context.Context, cfg config.Config, in doctorInput) check {
	if cfg.Renderer.Command == "" {
		return check{name: "renderer", ok: true, detail: "none configured"}
	}
	if err := in.runRenderer(ctx, cfg.Renderer.Command); err != nil {
		return check{name: "renderer", detail: err.Error()}
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
	file, existed, err := settings.Load(in.settingsPath)
	if err != nil || !existed {
		return check{name: name, ok: true, detail: "not set"}
	}
	member, err := settings.Get(file, "statusLine")
	if err != nil || !member.Found {
		return check{name: name, ok: true, detail: "not set"}
	}
	interval, err := settings.Get(member.Value, "refreshInterval")
	if err != nil || !interval.Found {
		return check{name: name, ok: true, detail: "not set"}
	}
	var seconds json.Number
	if json.Unmarshal(interval.Value, &seconds) != nil {
		return check{name: name, ok: true, detail: "not set"}
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
```

`status.go`: change `renderStatus(cfg, state, pending, dead int, newest string, now, zone)` and the dead-letters line to `fmt.Fprintf(&b, "Dead letters:      %d%s\n\n", dead, suffix)` where `suffix` is `" · newest: " + newest` when `newest != ""`. `runStatus` calls `store.ListDeadLetters` and passes `entries[0].Reason` when there is one (an error from `ListDeadLetters` is returned). The `quota.WindowStatus` values print as `observed`, `expired`, `unknown` through `%s`.

Add `case "doctor": return runDoctor(ctx, args[1:], info, streams)` and the usage line `  doctor [--settings path]  run every health check, exit 1 on failure` to `cli.go`. The `samplePayload` reset values are 2026-09-18T09:00:00Z and 2026-09-19T00:00:00Z as Unix seconds; the renderer only needs a valid shape.

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/cli/` then `make check`.
Expected: `ok`, lint clean. If a golden differs by one character, fix the check or the renderer, never the golden, unless the golden contradicts the row table above.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "$(cat <<'EOF'
feat: add doctor with every offline check and the newest dead letter

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Integration round trip

**Files:**
- Modify: `cmd/gaugewire/main_integration_test.go`

**Interfaces:**
- Consumes: the built binary, `GAUGEWIRE_HOME`, `--settings`.

- [ ] **Step 1: Write the tests**

Append (same build tag and package; reuse `buildBinary`, `statuslineOnce`, `waitForFlushRuns`):

```go
func gaugewire(t *testing.T, binary, home string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Env = append(os.Environ(), "GAUGEWIRE_HOME="+home)
	out, err := cmd.Output()
	return string(out), err
}

func TestInstallStatuslineUninstallRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs cat")
	}
	t.Parallel()
	binary := buildBinary(t, "")
	home := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	original := "{\n  \"model\": \"claude-sonnet-5\",\n  \"statusLine\": { \"type\": \"command\", \"command\": \"cat\" }\n}\n"
	if err := os.WriteFile(settingsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := gaugewire(t, binary, home, "install", "--settings", settingsPath, "--node-alias", "it-node"); err != nil {
		t.Fatalf("install: %v", err)
	}
	installed, err := os.ReadFile(settingsPath)
	if err != nil || !strings.Contains(string(installed), binary+" statusline") {
		t.Fatalf("settings after install: %q err %v", installed, err)
	}
	payload, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "statusline", "full.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	out, err := statuslineOnce(t, binary, home, payload)
	if err != nil || out != string(payload) {
		t.Fatalf("statusline: %q err %v", out, err)
	}
	if _, err := gaugewire(t, binary, home, "doctor", "--settings", settingsPath); err != nil {
		t.Fatalf("doctor after install should be healthy: %v", err)
	}
	if _, err := gaugewire(t, binary, home, "uninstall", "--settings", settingsPath); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	restored, err := os.ReadFile(settingsPath)
	if err != nil || string(restored) != original {
		t.Fatalf("settings after uninstall: %q err %v", restored, err)
	}
}

func TestDoctorExitsOneWhenUnhealthy(t *testing.T) {
	t.Parallel()
	binary := buildBinary(t, "")
	home := integrationHome(t, "", "[]")
	_, err := gaugewire(t, binary, home, "doctor", "--settings", filepath.Join(t.TempDir(), "settings.json"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("got %v, want exit code 1", err)
	}
}
```

Check `integrationHome`'s current signature (Plan 1b's last fix gave it a `sinks` parameter) and adapt the call. Add `"errors"` to the imports if missing. The round-trip test's statusline spools no event unless a sink is enabled; `install` creates a config with no sinks, so no flusher is spawned and nothing races cleanup.

- [ ] **Step 2: Run**

Run: `go test -race -tags integration -count=1 ./cmd/gaugewire/` then `make check`.
Expected: `ok`.

- [ ] **Step 3: Commit**

```bash
git add cmd/gaugewire
git commit -m "$(cat <<'EOF'
test: prove install, statusline and uninstall round-trip a settings file

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Docs and closing the plan

**Files:**
- Modify: `docs/spec/gaugewire/cli-and-install.md`, `docs/spec/gaugewire/architecture.md`, `docs/spec/gaugewire/testing-strategy.md`, `docs/spec/README.md`, `AGENTS.md`, `docs/nice-to-have.md`
- Create: `docs/how-tos/install-on-a-machine.md`
- Delete: `docs/plans/2026-09-18-install-uninstall-doctor.md`

- [ ] **Step 1: Make the pages state the tree**

Read each page against the code first.

- `cli-and-install.md`: marker `Draft · Built` and date; the Built/Not built sentence becomes "Built: every command except `databox bootstrap`. `doctor`'s sink auth, datasets and last ingestion rows arrive with the Databox sink." Install section: add "The status line already pointing at gaugewire is refused unless `--force`; `--force` keeps the saved original and refreshes the installed command. The backup is `settings.json.gaugewire-backup-<UTC timestamp>` with mode 0600. The executable path is quoted only when it contains whitespace; assumption to verify on Windows: PowerShell requires `& "path" statusline` for a quoted path, which the installed command does not emit yet." Uninstall: "`--settings` overrides the recorded path." doctor: mark the three sink rows "(with the Databox sink)" and add the output format sentence if missing; cite the public pages named in this plan's Spec line where the overrides row and the Windows shell sentence rely on them.
- `architecture.md`: add `internal/settings/   edit one member of Claude Code's settings file by byte offsets` to the layout and `settings → nothing` to the dependency line; `AGENTS.md` repository map and dependency direction get the same two additions.
- `testing-strategy.md`: add the `settings` row (fixture-driven goldens, byte-exact round trips) and the install round-trip integration test.
- `docs/spec/README.md`: gaugewire scope sentence: "Install, uninstall and doctor built; the Databox sink is next."
- `docs/nice-to-have.md`: remove the "status shows the newest dead-letter reason" entry (it shipped).
- `docs/how-tos/install-on-a-machine.md`: a short procedure: download the release binary, `gaugewire install --node-alias <name>`, `gaugewire doctor`, what the backup file is, `gaugewire uninstall` to revert. No third-party internals; only public docs cited.

- [ ] **Step 2: Delete this plan, verify, commit**

```bash
git rm docs/plans/2026-09-18-install-uninstall-doctor.md
```

Run the link check over touched files, the leak grep, and `make check`.

```bash
git add docs AGENTS.md
git commit -m "$(cat <<'EOF'
docs: mark install, uninstall and doctor as built

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

Then, with the owner's approval: push the branch and open the pull request with `gh pr create --assignee sulcer --label patch` and a Summary/Test plan body.
