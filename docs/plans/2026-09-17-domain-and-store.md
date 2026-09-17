# Domain and Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the pure quota domain (windows, reducer with the staleness guard, publish decision, snapshot contract), the Claude Code status-line parser, and the durable store (home directory, atomic writes, file locks, state, spool), fully unit-tested, with no CLI wiring yet.

**Architecture:** `internal/quota` knows nothing about files or processes: it reduces an `Observation` into a `State`, decides whether to publish, and builds `QuotaSnapshot v1`. `internal/source/claude` turns the status-line JSON into an `Observation`. `internal/store` owns the home directory, atomic file writes, advisory locks, `state.json` and the `pending/` and `dead-letter/` spool. A later plan wires them into `gaugewire statusline` and `gaugewire flush`.

**Tech Stack:** Go 1.27 standard library (`encoding/json`, `uuid`, `testing`), `github.com/gofrs/flock` v0.13.1 (locks), `github.com/google/go-cmp` v0.7.0 (tests only).

**Spec:** `docs/spec/gaugewire/data-contract.md`, `docs/spec/gaugewire/reducer-and-dedupe.md`, `docs/spec/gaugewire/spool-and-flush.md`, `docs/spec/gaugewire/testing-strategy.md`, `docs/spec/gaugewire/cli-and-install.md` (home directory section), `docs/spec/gaugewire/glossary.md`. Decisions: `docs/adr/2026-09-17-claude-statusline-is-the-only-quota-source.md`, `docs/adr/2026-09-17-json-config-in-one-home-directory.md`.

## Global Constraints

- Branch `feat/domain-and-store` from `main`. Commits are Conventional Commits without a scope, first line at most 72 characters, ending with the trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` via the heredoc form. The repo's git hooks are active; run `make check` before every commit.
- **Commit authority:** the owner approves every commit and every push unless autonomous mode was granted for the session. Never push without approval.
- Dependencies: only `github.com/gofrs/flock v0.13.1` (runtime) and `github.com/google/go-cmp v0.7.0` (tests) may be added. Everything else is standard library. Go 1.27's standard `uuid` package provides event ids.
- Dependency direction: `source/claude → quota`; `quota → nothing`; `store → quota`. Nothing in this plan imports `internal/cli`.
- Tests: standard `testing`; `t.Parallel()` in every test unless it mutates process state (`t.Setenv` tests must not call `t.Parallel`); one assertion per test comparing a whole value with `==` or `cmp.Diff`; expected values come from this plan's tables and fixtures, never from running the code; test names state the behaviour.
- Coverage gate: `internal/quota` and `internal/source/claude` must reach at least 90 % (`make cover` enforces it once the packages exist).
- Timestamps are UTC. Percentages are `float64`, never rounded. Absent is never zero.
- Files are created with the Write tool, never a shell heredoc. Every Go file is gofumpt-formatted (the Claude Code edit hook does it; `make fmt` otherwise).
- Third-party behaviour is relied on only when its public documentation states it; anything else is an assumption to verify.

## Before you start

```bash
git switch main && git pull --ff-only
git switch -c feat/domain-and-store
go get github.com/google/go-cmp@v0.7.0
go mod tidy
```

`go-cmp` becomes a direct requirement once the first test imports it (Task 1). `flock` is added in Task 6.

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/quota/window.go` | `WindowStatus`, `Window`, `Windows`, `Reading` |
| `internal/quota/state.go` | `State`, `Published`, `NewState`, `MarkPublished` |
| `internal/quota/reducer.go` | `Observation`, `Reduce`, the per-window state machine and staleness guard |
| `internal/quota/dedupe.go` | `EventType`, `Publishing`, `Decision`, `Decide` |
| `internal/quota/snapshot.go` | `Identity`, `Node`, `Account`, `Source`, `Snapshot`, `NewSnapshot` |
| `internal/quota/*_test.go` | one test file per source file |
| `internal/source/claude/parse.go` | `Parse`, version gate, per-window validation |
| `internal/source/claude/parse_test.go`, `fuzz_test.go` | table tests over fixtures, fuzz test |
| `fixtures/statusline/*.json` | real-shaped and synthetic stdin payloads |
| `internal/store/home.go` | `Home`, `EnsureLayout`, directory names |
| `internal/store/atomic.go` | `WriteFileAtomic` |
| `internal/store/lock.go` | `Lock`, `ErrLockTimeout` |
| `internal/store/state.go` | `State`, `FlushRecord`, `IngestionRecord`, `LoadState`, `SaveState`, `ErrStateCorrupt` |
| `internal/store/spool.go` | `Event`, `DeliveryState`, `PendingEvent`, `WritePending`, `ListPending`, `UpdatePending`, `DeletePending`, `DeadLetter`, `Requeue`, `Counts` |
| `internal/store/*_test.go` | one test file per source file |

---

### Task 1: Windows and the reducer

**Files:**
- Create: `internal/quota/window.go`, `internal/quota/state.go`, `internal/quota/reducer.go`, `internal/quota/reducer_test.go`

**Interfaces:**
- Produces: `quota.WindowStatus` with `WindowUnknown`, `WindowObserved`, `WindowExpired`; `quota.Window{Status, UsedPercentage *float64, ResetsAt *time.Time}`; `quota.Windows{FiveHour, SevenDay Window}`; `quota.Reading{UsedPercentage float64, ResetsAt time.Time}`; `quota.Observation{CapturedAt time.Time, ClaudeCodeVersion string, FiveHour, SevenDay *Reading}`; `quota.State{SchemaVersion int, Windows, LastObservedAt *time.Time, ClaudeCodeVersion string, LastPublished *Published}`; `quota.Published{EventID string, CapturedAt time.Time, Windows}`; `quota.NewState() State`; `quota.Reduce(state State, obs Observation) State`.

- [ ] **Step 1: Write the failing reducer tests**

`internal/quota/reducer_test.go`:

```go
package quota

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func ptr[T any](v T) *T { return &v }

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("bad test time %q: %v", value, err)
	}
	return parsed
}

func observed(used float64, resetsAt time.Time) Window {
	return Window{Status: WindowObserved, UsedPercentage: &used, ResetsAt: &resetsAt}
}

func TestReduceWindowStateMachine(t *testing.T) {
	t.Parallel()
	now := at(t, "2026-09-17T15:00:00Z")
	future := at(t, "2026-09-17T18:00:00Z")
	past := at(t, "2026-09-17T14:00:00Z")
	later := at(t, "2026-09-17T23:00:00Z")

	cases := []struct {
		name     string
		stored   Window
		incoming *Reading
		want     Window
	}{
		{"absent and never observed stays unknown", Window{}, nil, Window{Status: WindowUnknown}},
		{"absent and explicitly unknown stays unknown", Window{Status: WindowUnknown}, nil, Window{Status: WindowUnknown}},
		{"first valid reading is observed", Window{Status: WindowUnknown}, &Reading{42, future}, observed(42, future)},
		{"zero percent is a real value", Window{Status: WindowUnknown}, &Reading{0, future}, observed(0, future)},
		{"hundred percent is preserved", Window{Status: WindowUnknown}, &Reading{100, future}, observed(100, future)},
		{"decimal percentage is not rounded", Window{Status: WindowUnknown}, &Reading{41.2, future}, observed(41.2, future)},
		{"absent with an unexpired reset keeps the stored window", observed(42, future), nil, observed(42, future)},
		{"absent with a passed reset expires and keeps the reset for diagnostics", observed(42, past), nil, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired stays expired while absent", Window{Status: WindowExpired, ResetsAt: &past}, nil, Window{Status: WindowExpired, ResetsAt: &past}},
		{"expired then a fresh reading is observed again", Window{Status: WindowExpired, ResetsAt: &past}, &Reading{7, later}, observed(7, later)},
		{"same reset and lower usage is a stale session and is ignored", observed(70, future), &Reading{40, future}, observed(70, future)},
		{"same reset and higher usage is accepted", observed(40, future), &Reading{70, future}, observed(70, future)},
		{"same reset and equal usage is accepted", observed(40, future), &Reading{40, future}, observed(40, future)},
		{"newer reset with lower usage is a new window and is accepted", observed(70, future), &Reading{3, later}, observed(3, later)},
		{"older reset is a stale session and is ignored", observed(70, later), &Reading{90, future}, observed(70, later)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := reduceWindow(tc.stored, tc.incoming, now)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("window mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestReduceRecordsObservationMetadataAndBothWindows(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-17T15:30:00Z")
	fiveReset := at(t, "2026-09-17T16:20:00Z")
	sevenReset := at(t, "2026-09-18T07:00:00Z")
	obs := Observation{
		CapturedAt:        captured,
		ClaudeCodeVersion: "2.1.274",
		FiveHour:          &Reading{UsedPercentage: 24, ResetsAt: fiveReset},
		SevenDay:          &Reading{UsedPercentage: 53, ResetsAt: sevenReset},
	}
	got := Reduce(NewState(), obs)
	want := State{
		SchemaVersion: 1,
		Windows: Windows{
			FiveHour: observed(24, fiveReset),
			SevenDay: observed(53, sevenReset),
		},
		LastObservedAt:    &captured,
		ClaudeCodeVersion: "2.1.274",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestReduceWithOnlySevenDayLeavesFiveHourUnknown(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-17T15:30:00Z")
	sevenReset := at(t, "2026-09-18T07:00:00Z")
	obs := Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274", SevenDay: &Reading{UsedPercentage: 53, ResetsAt: sevenReset}}
	got := Reduce(NewState(), obs)
	want := State{
		SchemaVersion:     1,
		Windows:           Windows{FiveHour: Window{Status: WindowUnknown}, SevenDay: observed(53, sevenReset)},
		LastObservedAt:    &captured,
		ClaudeCodeVersion: "2.1.274",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestReducePreservesLastPublished(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-17T15:30:00Z")
	published := &Published{EventID: "e1", CapturedAt: at(t, "2026-09-17T15:00:00Z"), Windows: Windows{FiveHour: Window{Status: WindowUnknown}, SevenDay: Window{Status: WindowUnknown}}}
	state := NewState()
	state.LastPublished = published
	got := Reduce(state, Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274"})
	if diff := cmp.Diff(published, got.LastPublished); diff != "" {
		t.Fatalf("lastPublished mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/quota/`
Expected: FAIL to build, `undefined: Window`, `undefined: Reading`, `undefined: reduceWindow`, `undefined: Reduce`, `undefined: NewState`, `undefined: State`.

- [ ] **Step 3: Write the types**

`internal/quota/window.go`:

```go
// Package quota is the pure domain of Gaugewire: rate-limit windows, the reducer
// that folds observations into machine state, the publish decision, and the
// QuotaSnapshot v1 contract. It performs no I/O.
package quota

import "time"

// WindowStatus says what is known about one rate-limit window.
type WindowStatus string

const (
	// WindowUnknown means the window has never been observed.
	WindowUnknown WindowStatus = "unknown"
	// WindowObserved means a valid value is held.
	WindowObserved WindowStatus = "observed"
	// WindowExpired means the stored reset time passed and no fresh value arrived.
	WindowExpired WindowStatus = "expired"
)

// Window is the stored state of one rate-limit window. UsedPercentage is nil
// unless the status is observed; ResetsAt is kept through expiry for diagnostics.
type Window struct {
	Status         WindowStatus `json:"status"`
	UsedPercentage *float64     `json:"usedPercentage"`
	ResetsAt       *time.Time   `json:"resetsAt"`
}

// Windows holds both subscription windows.
type Windows struct {
	FiveHour Window `json:"fiveHour"`
	SevenDay Window `json:"sevenDay"`
}

// Reading is one valid window value reported by a source.
type Reading struct {
	UsedPercentage float64
	ResetsAt       time.Time
}
```

`internal/quota/state.go`:

```go
package quota

import "time"

// SchemaVersion is the version of State and Snapshot; a breaking change bumps it.
const SchemaVersion = 1

// State is the machine-wide quota state persisted between invocations.
type State struct {
	SchemaVersion     int        `json:"schemaVersion"`
	Windows           Windows    `json:"windows"`
	LastObservedAt    *time.Time `json:"lastObservedAt"`
	ClaudeCodeVersion string     `json:"claudeCodeVersion"`
	LastPublished     *Published `json:"lastPublished"`
}

// Published records the snapshot most recently spooled for delivery.
type Published struct {
	EventID    string    `json:"eventId"`
	CapturedAt time.Time `json:"capturedAt"`
	Windows    Windows   `json:"windows"`
}

// NewState returns the state of a machine that has never observed anything.
func NewState() State {
	return State{
		SchemaVersion: SchemaVersion,
		Windows: Windows{
			FiveHour: Window{Status: WindowUnknown},
			SevenDay: Window{Status: WindowUnknown},
		},
	}
}

// MarkPublished records that a snapshot with the current windows was spooled.
func MarkPublished(state State, eventID string, capturedAt time.Time) State {
	state.LastPublished = &Published{EventID: eventID, CapturedAt: capturedAt, Windows: state.Windows}
	return state
}
```

`internal/quota/reducer.go`:

```go
package quota

import "time"

// Observation is what a source produced from one status-line invocation.
// A nil window means the source did not report it or reported it invalidly.
type Observation struct {
	CapturedAt        time.Time
	ClaudeCodeVersion string
	FiveHour          *Reading
	SevenDay          *Reading
}

// Reduce folds one observation into the state. It is pure: the caller persists
// the result. Each window is reduced independently.
func Reduce(state State, obs Observation) State {
	state.SchemaVersion = SchemaVersion
	state.Windows.FiveHour = reduceWindow(state.Windows.FiveHour, obs.FiveHour, obs.CapturedAt)
	state.Windows.SevenDay = reduceWindow(state.Windows.SevenDay, obs.SevenDay, obs.CapturedAt)
	captured := obs.CapturedAt
	state.LastObservedAt = &captured
	state.ClaudeCodeVersion = obs.ClaudeCodeVersion
	return state
}

func reduceWindow(stored Window, incoming *Reading, now time.Time) Window {
	if incoming != nil {
		if !accepts(stored, *incoming) {
			return stored
		}
		used := incoming.UsedPercentage
		resetsAt := incoming.ResetsAt
		return Window{Status: WindowObserved, UsedPercentage: &used, ResetsAt: &resetsAt}
	}
	if stored.ResetsAt == nil || stored.Status == WindowUnknown {
		return Window{Status: WindowUnknown}
	}
	if stored.ResetsAt.After(now) {
		return stored
	}
	return Window{Status: WindowExpired, ResetsAt: stored.ResetsAt}
}

// accepts is the staleness guard. Every Claude Code session re-sends its own
// last-known values, so an idle session must not overwrite an active one: an
// incoming value replaces an observed window only when its reset is newer, or
// equal with usage at or above the stored value.
func accepts(stored Window, incoming Reading) bool {
	if stored.Status != WindowObserved || stored.ResetsAt == nil || stored.UsedPercentage == nil {
		return true
	}
	if incoming.ResetsAt.After(*stored.ResetsAt) {
		return true
	}
	if incoming.ResetsAt.Equal(*stored.ResetsAt) {
		return incoming.UsedPercentage >= *stored.UsedPercentage
	}
	return false
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/quota/`
Expected: `ok`. Then `make check` (lint may ask for nothing; `revive` requires the doc comments above, which are present).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/quota
git commit -m "$(cat <<'EOF'
feat: add quota windows, state and the reducer

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Publish decision

**Files:**
- Create: `internal/quota/dedupe.go`, `internal/quota/dedupe_test.go`, `internal/quota/state_test.go`

**Interfaces:**
- Consumes: `State`, `Published`, `Windows`, `Window`, `NewState`, `MarkPublished` from Task 1.
- Produces: `quota.EventType` with `EventStateTransition` ("state_transition"), `EventChange` ("change"), `EventHeartbeat` ("heartbeat"); `quota.Publishing{MinDeltaPercentage float64, HeartbeatInterval time.Duration}`; `quota.DefaultPublishing() Publishing`; `quota.Decision{Publish bool, EventType EventType}`; `quota.Decide(state State, now time.Time, p Publishing) Decision`.

- [ ] **Step 1: Write the failing tests**

`internal/quota/dedupe_test.go`:

```go
package quota

import (
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	t.Parallel()
	reset := at(t, "2026-09-17T18:00:00Z")
	nextReset := at(t, "2026-09-17T23:00:00Z")
	publishedAt := at(t, "2026-09-17T15:00:00Z")
	soon := at(t, "2026-09-17T15:10:00Z")
	late := at(t, "2026-09-17T15:31:00Z")
	unknownWindows := Windows{FiveHour: Window{Status: WindowUnknown}, SevenDay: Window{Status: WindowUnknown}}
	published := func(w Windows) *Published {
		return &Published{EventID: "e1", CapturedAt: publishedAt, Windows: w}
	}
	withPublished := func(current Windows, last *Published) State {
		s := NewState()
		s.Windows = current
		s.LastPublished = last
		return s
	}
	both := func(five, seven float64) Windows {
		return Windows{FiveHour: observed(five, reset), SevenDay: observed(seven, reset)}
	}

	cases := []struct {
		name  string
		state State
		now   time.Time
		want  Decision
	}{
		{"never published and nothing observed does not publish", withPublished(unknownWindows, nil), soon, Decision{}},
		{"never published and one window observed publishes a state transition", withPublished(Windows{FiveHour: observed(24, reset), SevenDay: Window{Status: WindowUnknown}}, nil), soon, Decision{Publish: true, EventType: EventStateTransition}},
		{"a window status change publishes a state transition", withPublished(Windows{FiveHour: Window{Status: WindowExpired, ResetsAt: &reset}, SevenDay: observed(53, reset)}, published(both(40, 53))), soon, Decision{Publish: true, EventType: EventStateTransition}},
		{"a reset change publishes a change", withPublished(Windows{FiveHour: observed(2, nextReset), SevenDay: observed(53, reset)}, published(both(40, 53))), soon, Decision{Publish: true, EventType: EventChange}},
		{"a delta below the threshold does not publish", withPublished(both(40.7, 53), published(both(40, 53))), soon, Decision{}},
		{"a delta at or above the threshold publishes a change", withPublished(both(41.1, 53), published(both(40, 53))), soon, Decision{Publish: true, EventType: EventChange}},
		{"a drop of the threshold in the seven day window publishes a change", withPublished(both(40, 51.9), published(both(40, 53))), soon, Decision{Publish: true, EventType: EventChange}},
		{"nothing changed before the heartbeat interval does not publish", withPublished(both(40, 53), published(both(40, 53))), soon, Decision{}},
		{"nothing changed after the heartbeat interval publishes a heartbeat", withPublished(both(40, 53), published(both(40, 53))), late, Decision{Publish: true, EventType: EventHeartbeat}},
		{"heartbeat needs an observed window", withPublished(Windows{FiveHour: Window{Status: WindowExpired, ResetsAt: &reset}, SevenDay: Window{Status: WindowExpired, ResetsAt: &reset}}, published(Windows{FiveHour: Window{Status: WindowExpired, ResetsAt: &reset}, SevenDay: Window{Status: WindowExpired, ResetsAt: &reset}})), late, Decision{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Decide(tc.state, tc.now, DefaultPublishing())
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestDecideComparesAgainstLastPublishedNotPreviousInput(t *testing.T) {
	t.Parallel()
	reset := at(t, "2026-09-17T18:00:00Z")
	last := &Published{EventID: "e1", CapturedAt: at(t, "2026-09-17T15:00:00Z"), Windows: Windows{FiveHour: observed(40, reset), SevenDay: observed(53, reset)}}
	state := NewState()
	state.LastPublished = last
	// Three small steps that each stay under the threshold relative to the
	// previous input but accumulate past it relative to the last published value.
	steps := []float64{40.2, 40.4, 40.7, 41.1}
	got := make([]bool, 0, len(steps))
	for _, used := range steps {
		state.Windows = Windows{FiveHour: observed(used, reset), SevenDay: observed(53, reset)}
		got = append(got, Decide(state, at(t, "2026-09-17T15:05:00Z"), DefaultPublishing()).Publish)
	}
	want := []bool{false, false, false, true}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("publish sequence %v, want %v", got, want)
	}
}
```

`internal/quota/state_test.go`:

```go
package quota

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMarkPublishedSnapshotsTheCurrentWindows(t *testing.T) {
	t.Parallel()
	reset := at(t, "2026-09-17T18:00:00Z")
	captured := at(t, "2026-09-17T15:30:00Z")
	state := NewState()
	state.Windows = Windows{FiveHour: observed(24, reset), SevenDay: Window{Status: WindowUnknown}}
	got := MarkPublished(state, "event-1", captured)
	want := &Published{EventID: "event-1", CapturedAt: captured, Windows: Windows{FiveHour: observed(24, reset), SevenDay: Window{Status: WindowUnknown}}}
	if diff := cmp.Diff(want, got.LastPublished); diff != "" {
		t.Fatalf("lastPublished mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/quota/`
Expected: FAIL to build, `undefined: Decide`, `undefined: Decision`, `undefined: DefaultPublishing`, `undefined: EventStateTransition`.

- [ ] **Step 3: Write `internal/quota/dedupe.go`**

```go
package quota

import (
	"math"
	"time"
)

// EventType classifies why a snapshot is published.
type EventType string

const (
	// EventStateTransition is the first observation or a window status change.
	EventStateTransition EventType = "state_transition"
	// EventChange is a reset change or a usage move past the threshold.
	EventChange EventType = "change"
	// EventHeartbeat is a periodic publish while nothing changed.
	EventHeartbeat EventType = "heartbeat"
)

// Publishing holds the dedupe thresholds.
type Publishing struct {
	MinDeltaPercentage float64
	HeartbeatInterval  time.Duration
}

// DefaultPublishing returns the spec defaults: 1 percentage point, 30 minutes.
func DefaultPublishing() Publishing {
	return Publishing{MinDeltaPercentage: 1.0, HeartbeatInterval: 30 * time.Minute}
}

// Decision says whether the current state should be published and why.
type Decision struct {
	Publish   bool
	EventType EventType
}

// Decide compares the reduced state with the last published snapshot. The first
// matching rule wins; usage is compared against the last published value, not
// the previous input, so small drifts accumulate.
func Decide(state State, now time.Time, p Publishing) Decision {
	anyObserved := state.Windows.FiveHour.Status == WindowObserved || state.Windows.SevenDay.Status == WindowObserved
	if state.LastPublished == nil {
		if anyObserved {
			return Decision{Publish: true, EventType: EventStateTransition}
		}
		return Decision{}
	}
	last := state.LastPublished.Windows
	current := state.Windows
	if current.FiveHour.Status != last.FiveHour.Status || current.SevenDay.Status != last.SevenDay.Status {
		return Decision{Publish: true, EventType: EventStateTransition}
	}
	if !sameTime(current.FiveHour.ResetsAt, last.FiveHour.ResetsAt) || !sameTime(current.SevenDay.ResetsAt, last.SevenDay.ResetsAt) {
		return Decision{Publish: true, EventType: EventChange}
	}
	if delta(current.FiveHour.UsedPercentage, last.FiveHour.UsedPercentage) >= p.MinDeltaPercentage ||
		delta(current.SevenDay.UsedPercentage, last.SevenDay.UsedPercentage) >= p.MinDeltaPercentage {
		return Decision{Publish: true, EventType: EventChange}
	}
	if anyObserved && now.Sub(state.LastPublished.CapturedAt) >= p.HeartbeatInterval {
		return Decision{Publish: true, EventType: EventHeartbeat}
	}
	return Decision{}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func delta(a, b *float64) float64 {
	if a == nil || b == nil {
		return 0
	}
	return math.Abs(*a - *b)
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/quota/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/quota
git commit -m "$(cat <<'EOF'
feat: add the publish decision with threshold and heartbeat

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: QuotaSnapshot v1

**Files:**
- Create: `internal/quota/snapshot.go`, `internal/quota/snapshot_test.go`

**Interfaces:**
- Consumes: `State`, `Windows` from Task 1.
- Produces: `quota.SourceTypeClaudeCodeStatusline` ("claude-code-statusline"); `quota.Identity{NodeID, NodeAlias, Platform, AccountID, AccountAlias, ObserverVersion string}`; `quota.Node{ID, Alias, Platform}`; `quota.Account{ID, Alias}`; `quota.Source{Type, ClaudeCodeVersion}`; `quota.Snapshot{SchemaVersion, EventID, Node, Account, CapturedAt, Windows, Source, ObserverVersion}`; `quota.NewSnapshot(id Identity, state State, eventID string, capturedAt time.Time) Snapshot`.

- [ ] **Step 1: Write the failing test**

`internal/quota/snapshot_test.go`. The expected JSON is the data contract from `docs/spec/gaugewire/data-contract.md`, with `53.5` instead of `53` to prove decimals survive and second-precision timestamps.

```go
package quota

import (
	"encoding/json"
	"testing"
)

func TestSnapshotJSONMatchesTheContract(t *testing.T) {
	t.Parallel()
	captured := at(t, "2026-09-17T15:30:00Z")
	state := NewState()
	state.Windows = Windows{
		FiveHour: observed(24, at(t, "2026-09-17T16:20:00Z")),
		SevenDay: observed(53.5, at(t, "2026-09-18T07:00:00Z")),
	}
	state.ClaudeCodeVersion = "2.1.274"
	identity := Identity{NodeID: "node-uuid", NodeAlias: "mac-mini-01", Platform: "darwin", AccountID: "account-uuid", AccountAlias: "claude-01", ObserverVersion: "1.0.0"}
	got, err := json.Marshal(NewSnapshot(identity, state, "event-uuid", captured))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"schemaVersion":1,"eventId":"event-uuid",` +
		`"node":{"id":"node-uuid","alias":"mac-mini-01","platform":"darwin"},` +
		`"account":{"id":"account-uuid","alias":"claude-01"},` +
		`"capturedAt":"2026-09-17T15:30:00Z",` +
		`"windows":{"fiveHour":{"status":"observed","usedPercentage":24,"resetsAt":"2026-09-17T16:20:00Z"},` +
		`"sevenDay":{"status":"observed","usedPercentage":53.5,"resetsAt":"2026-09-18T07:00:00Z"}},` +
		`"source":{"type":"claude-code-statusline","claudeCodeVersion":"2.1.274"},` +
		`"observerVersion":"1.0.0"}`
	if string(got) != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestSnapshotJSONKeepsUnknownWindowsAsNulls(t *testing.T) {
	t.Parallel()
	state := NewState()
	got, err := json.Marshal(NewSnapshot(Identity{}, state, "e", at(t, "2026-09-17T15:30:00Z")).Windows)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"fiveHour":{"status":"unknown","usedPercentage":null,"resetsAt":null},` +
		`"sevenDay":{"status":"unknown","usedPercentage":null,"resetsAt":null}}`
	if string(got) != want {
		t.Fatalf("json mismatch\n got: %s\nwant: %s", got, want)
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/quota/`
Expected: FAIL to build, `undefined: Identity`, `undefined: NewSnapshot`.

- [ ] **Step 3: Write `internal/quota/snapshot.go`**

```go
package quota

import "time"

// SourceTypeClaudeCodeStatusline names the only v1 source.
const SourceTypeClaudeCodeStatusline = "claude-code-statusline"

// Identity is the configured node and account identity; it is never inferred.
type Identity struct {
	NodeID          string
	NodeAlias       string
	Platform        string
	AccountID       string
	AccountAlias    string
	ObserverVersion string
}

// Node identifies the machine.
type Node struct {
	ID       string `json:"id"`
	Alias    string `json:"alias"`
	Platform string `json:"platform"`
}

// Account identifies the Claude subscription logged into the node.
type Account struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

// Source says where the observation came from.
type Source struct {
	Type              string `json:"type"`
	ClaudeCodeVersion string `json:"claudeCodeVersion"`
}

// Snapshot is QuotaSnapshot v1, the only shape that leaves the machine.
type Snapshot struct {
	SchemaVersion   int       `json:"schemaVersion"`
	EventID         string    `json:"eventId"`
	Node            Node      `json:"node"`
	Account         Account   `json:"account"`
	CapturedAt      time.Time `json:"capturedAt"`
	Windows         Windows   `json:"windows"`
	Source          Source    `json:"source"`
	ObserverVersion string    `json:"observerVersion"`
}

// NewSnapshot builds the snapshot for the current state.
func NewSnapshot(id Identity, state State, eventID string, capturedAt time.Time) Snapshot {
	return Snapshot{
		SchemaVersion:   SchemaVersion,
		EventID:         eventID,
		Node:            Node{ID: id.NodeID, Alias: id.NodeAlias, Platform: id.Platform},
		Account:         Account{ID: id.AccountID, Alias: id.AccountAlias},
		CapturedAt:      capturedAt.UTC(),
		Windows:         state.Windows,
		Source:          Source{Type: SourceTypeClaudeCodeStatusline, ClaudeCodeVersion: state.ClaudeCodeVersion},
		ObserverVersion: id.ObserverVersion,
	}
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/quota/` then `make cover` (the gate now applies to `internal/quota`; expect at least 90 %).
Expected: `ok`; gate line `coverage-gate: ./internal/quota <n>%` with n ≥ 90.

- [ ] **Step 5: Commit**

```bash
git add internal/quota
git commit -m "$(cat <<'EOF'
feat: add the quota snapshot contract

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Status-line parser and fixtures

**Files:**
- Create: `internal/source/claude/parse.go`, `internal/source/claude/parse_test.go`, `internal/source/claude/fuzz_test.go`, nine files under `fixtures/statusline/`

**Interfaces:**
- Consumes: `quota.Observation`, `quota.Reading`.
- Produces: `claude.MinimumVersion` ("2.1.251"); `claude.ErrUnsupportedVersion`; `claude.ErrInvalidPayload`; `claude.Parse(r io.Reader, capturedAt time.Time) (quota.Observation, []string, error)` where the string slice lists the JSON paths of windows that were present but invalid and were therefore treated as absent.

- [ ] **Step 1: Write the fixtures**

`fixtures/statusline/full.json` (the documented shape, unrelated fields included on purpose):

```json
{
  "cwd": "/home/user/project",
  "session_id": "abc123",
  "transcript_path": "/home/user/.claude/projects/x/transcript.jsonl",
  "model": { "id": "claude-opus-5", "display_name": "Opus" },
  "version": "2.1.274",
  "context_window": { "used_percentage": 8 },
  "rate_limits": {
    "five_hour": { "used_percentage": 23.5, "resets_at": 1789659600 },
    "seven_day": { "used_percentage": 41.2, "resets_at": 1789714800 },
    "spend_limit": { "used_percentage": 62.8, "resets_at": 1790000000 }
  }
}
```

`fixtures/statusline/no-rate-limits.json`:

```json
{ "version": "2.1.274", "session_id": "abc123", "model": { "id": "claude-opus-5" } }
```

`fixtures/statusline/five-hour-only.json`:

```json
{ "version": "2.1.274", "rate_limits": { "five_hour": { "used_percentage": 24, "resets_at": 1789659600 } } }
```

`fixtures/statusline/seven-day-only.json`:

```json
{ "version": "2.1.274", "rate_limits": { "seven_day": { "used_percentage": 53, "resets_at": 1789714800 } } }
```

`fixtures/statusline/zero-and-hundred.json`:

```json
{ "version": "2.1.274", "rate_limits": { "five_hour": { "used_percentage": 0, "resets_at": 1789659600 }, "seven_day": { "used_percentage": 100, "resets_at": 1789714800 } } }
```

`fixtures/statusline/malformed-percentage.json`:

```json
{ "version": "2.1.274", "rate_limits": { "five_hour": { "used_percentage": "lots", "resets_at": 1789659600 }, "seven_day": { "used_percentage": 101, "resets_at": 1789714800 } } }
```

`fixtures/statusline/malformed-reset.json`:

```json
{ "version": "2.1.274", "rate_limits": { "five_hour": { "used_percentage": 24, "resets_at": "tomorrow" }, "seven_day": { "used_percentage": 53 } } }
```

`fixtures/statusline/old-version.json`:

```json
{ "version": "2.1.250", "rate_limits": { "five_hour": { "used_percentage": 24, "resets_at": 1789659600 } } }
```

`fixtures/statusline/missing-version.json`:

```json
{ "rate_limits": { "five_hour": { "used_percentage": 24, "resets_at": 1789659600 } } }
```

- [ ] **Step 2: Write the failing tests**

`internal/source/claude/parse_test.go`:

```go
package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/quota"
)

var captured = time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)

func fixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "..", "fixtures", "statusline", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func epoch(seconds int64) time.Time { return time.Unix(seconds, 0).UTC() }

type parsed struct {
	obs    quota.Observation
	issues []string
	err    error
}

func TestParseFixtures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fixture string
		want    parsed
	}{
		{"full.json", parsed{
			obs: quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274",
				FiveHour: &quota.Reading{UsedPercentage: 23.5, ResetsAt: epoch(1789659600)},
				SevenDay: &quota.Reading{UsedPercentage: 41.2, ResetsAt: epoch(1789714800)}},
		}},
		{"no-rate-limits.json", parsed{obs: quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274"}}},
		{"five-hour-only.json", parsed{obs: quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274",
			FiveHour: &quota.Reading{UsedPercentage: 24, ResetsAt: epoch(1789659600)}}}},
		{"seven-day-only.json", parsed{obs: quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274",
			SevenDay: &quota.Reading{UsedPercentage: 53, ResetsAt: epoch(1789714800)}}}},
		{"zero-and-hundred.json", parsed{obs: quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274",
			FiveHour: &quota.Reading{UsedPercentage: 0, ResetsAt: epoch(1789659600)},
			SevenDay: &quota.Reading{UsedPercentage: 100, ResetsAt: epoch(1789714800)}}}},
		{"malformed-percentage.json", parsed{
			obs:    quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274"},
			issues: []string{"rate_limits.five_hour", "rate_limits.seven_day"},
		}},
		{"malformed-reset.json", parsed{
			obs:    quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.274"},
			issues: []string{"rate_limits.five_hour", "rate_limits.seven_day"},
		}},
		{"old-version.json", parsed{err: ErrUnsupportedVersion}},
		{"missing-version.json", parsed{err: ErrUnsupportedVersion}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			obs, issues, err := Parse(fixture(t, tc.fixture), captured)
			got := parsed{obs: obs, issues: issues, err: err}
			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(parsed{}), cmp.Comparer(func(a, b error) bool { return errors.Is(a, b) || errors.Is(b, a) })); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseRejectsNonJSON(t *testing.T) {
	t.Parallel()
	_, _, err := Parse(strings.NewReader("not json"), captured)
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("got %v, want ErrInvalidPayload", err)
	}
}

func TestParseRejectsEmptyInput(t *testing.T) {
	t.Parallel()
	_, _, err := Parse(strings.NewReader(""), captured)
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("got %v, want ErrInvalidPayload", err)
	}
}

func TestParseAcceptsTheMinimumVersion(t *testing.T) {
	t.Parallel()
	obs, _, err := Parse(strings.NewReader(`{"version":"2.1.251"}`), captured)
	got := parsed{obs: obs, err: err}
	want := parsed{obs: quota.Observation{CapturedAt: captured, ClaudeCodeVersion: "2.1.251"}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(parsed{})); diff != "" {
		t.Fatalf("mismatch (-want +got):\n%s", diff)
	}
}

func TestParseTreatsAnUnparseableVersionAsUnsupported(t *testing.T) {
	t.Parallel()
	_, _, err := Parse(strings.NewReader(`{"version":"nightly"}`), captured)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("got %v, want ErrUnsupportedVersion", err)
	}
}
```

`internal/source/claude/fuzz_test.go`:

```go
package claude

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func FuzzParse(f *testing.F) {
	names := []string{"full.json", "no-rate-limits.json", "malformed-percentage.json", "malformed-reset.json", "old-version.json"}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "statusline", name))
		if err != nil {
			f.Fatalf("read fixture: %v", err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"version":"2.1.274","rate_limits":{"five_hour":{"used_percentage":-1,"resets_at":0}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		obs, issues, err := Parse(bytes.NewReader(data), time.Unix(0, 0).UTC())
		if err != nil {
			return
		}
		if obs.FiveHour != nil && (obs.FiveHour.UsedPercentage < 0 || obs.FiveHour.UsedPercentage > 100 || obs.FiveHour.ResetsAt.Unix() <= 0) {
			t.Fatalf("invalid five hour reading accepted: %+v", obs.FiveHour)
		}
		if obs.SevenDay != nil && (obs.SevenDay.UsedPercentage < 0 || obs.SevenDay.UsedPercentage > 100 || obs.SevenDay.ResetsAt.Unix() <= 0) {
			t.Fatalf("invalid seven day reading accepted: %+v", obs.SevenDay)
		}
		if len(issues) > 2 {
			t.Fatalf("more issues than windows: %v", issues)
		}
	})
}
```

- [ ] **Step 3: Run the tests and watch them fail**

Run: `go test ./internal/source/claude/`
Expected: FAIL to build, `undefined: Parse`, `undefined: ErrUnsupportedVersion`, `undefined: ErrInvalidPayload`.

- [ ] **Step 4: Write `internal/source/claude/parse.go`**

```go
// Package claude turns Claude Code's status-line JSON into a quota observation.
// Only the version and the two subscription windows are read; everything else
// in the payload is ignored and never retained.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

// MinimumVersion is the oldest Claude Code whose status line carries rate limits
// in the documented shape.
const MinimumVersion = "2.1.251"

var (
	// ErrUnsupportedVersion means the payload's version is missing, unparseable
	// or older than MinimumVersion; the observation must be skipped.
	ErrUnsupportedVersion = errors.New("claude code version is missing or older than " + MinimumVersion)
	// ErrInvalidPayload means stdin did not hold a JSON object.
	ErrInvalidPayload = errors.New("status-line payload is not a JSON object")
)

type payload struct {
	Version    string `json:"version"`
	RateLimits struct {
		FiveHour json.RawMessage `json:"five_hour"`
		SevenDay json.RawMessage `json:"seven_day"`
	} `json:"rate_limits"`
}

type rawWindow struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *int64   `json:"resets_at"`
}

// Parse reads one status-line payload. It returns the observation, the JSON
// paths of windows that were present but invalid (treated as absent), and an
// error when the payload is unusable as a whole.
func Parse(r io.Reader, capturedAt time.Time) (quota.Observation, []string, error) {
	var p payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return quota.Observation{}, nil, fmt.Errorf("%w: %w", ErrInvalidPayload, err)
	}
	if !versionAtLeast(p.Version, MinimumVersion) {
		return quota.Observation{}, nil, ErrUnsupportedVersion
	}
	obs := quota.Observation{CapturedAt: capturedAt.UTC(), ClaudeCodeVersion: p.Version}
	var issues []string
	obs.FiveHour, issues = parseWindow(p.RateLimits.FiveHour, "rate_limits.five_hour", issues)
	obs.SevenDay, issues = parseWindow(p.RateLimits.SevenDay, "rate_limits.seven_day", issues)
	return obs, issues, nil
}

// parseWindow returns nil for an absent window and nil plus an issue for an
// invalid one. Absent is never zero.
func parseWindow(raw json.RawMessage, path string, issues []string) (*quota.Reading, []string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, issues
	}
	var w rawWindow
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, append(issues, path)
	}
	if w.UsedPercentage == nil || w.ResetsAt == nil {
		return nil, append(issues, path)
	}
	if *w.UsedPercentage < 0 || *w.UsedPercentage > 100 || *w.ResetsAt <= 0 {
		return nil, append(issues, path)
	}
	return &quota.Reading{UsedPercentage: *w.UsedPercentage, ResetsAt: time.Unix(*w.ResetsAt, 0).UTC()}, issues
}

// versionAtLeast compares dotted numeric versions such as "2.1.274". Anything
// that is not three non-negative integers is treated as unsupported.
func versionAtLeast(version, minimum string) bool {
	have, ok := parseVersion(version)
	if !ok {
		return false
	}
	want, _ := parseVersion(minimum)
	for i := range 3 {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return true
}

func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimSpace(s), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
```

`fmt.Errorf` with two `%w` verbs is valid since Go 1.20. `for i := range 3` is valid since Go 1.22.

- [ ] **Step 5: Run the tests, the fuzz seeds and the coverage gate**

Run: `go test -race -count=1 ./internal/source/claude/` then `go test -run=^$ -fuzz=FuzzParse -fuzztime=10s ./internal/source/claude/` then `make cover` then `make check`.
Expected: `ok`; the fuzz run reports no failure; the gate prints at least 90 % for both `internal/quota` and `internal/source/claude`; lint clean. If the fuzzer finds a crash it writes a file under `testdata/fuzz/FuzzParse/`; fix the parser and keep that file as a regression case.

- [ ] **Step 6: Commit**

```bash
git add fixtures internal/source
git commit -m "$(cat <<'EOF'
feat: parse the claude code status line into an observation

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Home directory and atomic writes

**Files:**
- Create: `internal/store/home.go`, `internal/store/home_test.go`, `internal/store/atomic.go`, `internal/store/atomic_test.go`

**Interfaces:**
- Produces: `store.HomeEnv` ("GAUGEWIRE_HOME"); `store.Home() (string, error)`; `store.EnsureLayout(home string) error`; `store.PendingDir`, `store.DeadLetterDir`, `store.LogsDir` (names "pending", "dead-letter", "logs"); `store.WriteFileAtomic(path string, data []byte, perm fs.FileMode) error`.

- [ ] **Step 1: Write the failing tests**

`internal/store/home_test.go`:

```go
package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHomeHonoursTheEnvironmentOverride(t *testing.T) {
	t.Setenv(HomeEnv, "/tmp/gaugewire-override")
	got, err := Home()
	if err != nil || got != "/tmp/gaugewire-override" {
		t.Fatalf("got %q, %v; want /tmp/gaugewire-override, nil", got, err)
	}
}

func TestHomeDefaultsToTheUserConfigDir(t *testing.T) {
	t.Setenv(HomeEnv, "")
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("no user config dir on this machine: %v", err)
	}
	got, err := Home()
	want := filepath.Join(configDir, "gaugewire")
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q, nil", got, err, want)
	}
}

func TestEnsureLayoutCreatesThePrivateDirectories(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "gaugewire")
	if err := EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	got := make(map[string]bool)
	for _, dir := range []string{"", PendingDir, DeadLetterDir, LogsDir} {
		info, err := os.Stat(filepath.Join(home, dir))
		got[dir] = err == nil && info.IsDir()
	}
	want := map[string]bool{"": true, PendingDir: true, DeadLetterDir: true, LogsDir: true}
	if len(got) != len(want) || !got[""] || !got[PendingDir] || !got[DeadLetterDir] || !got[LogsDir] {
		t.Fatalf("directories %v, want %v", got, want)
	}
}

func TestEnsureLayoutIsIdempotent(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "gaugewire")
	first := EnsureLayout(home)
	second := EnsureLayout(home)
	if first != nil || second != nil {
		t.Fatalf("errors %v, %v; want nil, nil", first, second)
	}
}
```

`internal/store/atomic_test.go`:

```go
package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicReplacesTheContent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteFileAtomic(path, []byte("one"), 0o600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteFileAtomic(path, []byte("two"), 0o600); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "two" {
		t.Fatalf("got %q, %v; want \"two\", nil", got, err)
	}
}

func TestWriteFileAtomicLeavesNoTemporaryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := WriteFileAtomic(filepath.Join(dir, "state.json"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "state.json" {
		t.Fatalf("directory holds %v, want [state.json]", names)
	}
}

func TestWriteFileAtomicFailsWhenTheDirectoryIsMissing(t *testing.T) {
	t.Parallel()
	err := WriteFileAtomic(filepath.Join(t.TempDir(), "missing", "state.json"), []byte("x"), 0o600)
	if err == nil {
		t.Fatal("got nil error, want a failure for a missing directory")
	}
}
```

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/store/`
Expected: FAIL to build, `undefined: Home`, `undefined: HomeEnv`, `undefined: EnsureLayout`, `undefined: WriteFileAtomic`.

- [ ] **Step 3: Write the implementation**

`internal/store/home.go`:

```go
// Package store owns everything Gaugewire keeps on disk: the home directory,
// atomic file writes, advisory locks, the machine state and the event spool.
package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// HomeEnv overrides the home directory when set.
const HomeEnv = "GAUGEWIRE_HOME"

// Directory names inside the home directory.
const (
	PendingDir    = "pending"
	DeadLetterDir = "dead-letter"
	LogsDir       = "logs"
)

const dirName = "gaugewire"

// Home returns the directory holding config, state, locks, spool and logs:
// GAUGEWIRE_HOME when set, else the platform user config dir plus "gaugewire".
func Home() (string, error) {
	if override := os.Getenv(HomeEnv); override != "" {
		return override, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(base, dirName), nil
}

// EnsureLayout creates the home directory and its subdirectories with owner-only
// permissions. It is safe to call on every run.
func EnsureLayout(home string) error {
	for _, dir := range []string{home, filepath.Join(home, PendingDir), filepath.Join(home, DeadLetterDir), filepath.Join(home, LogsDir)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return nil
}
```

`internal/store/atomic.go`:

```go
package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to a temporary file in the same directory, syncs
// it, and renames it over path, so a crash never leaves a partial file.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		cleanup()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/store/` then `make check`.
Expected: `ok`, lint clean. (`gosec` may warn about `0o700` directory permissions being too permissive only when above 0750; 0700 is fine.)

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "$(cat <<'EOF'
feat: add the home directory layout and atomic file writes

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: File locks

**Files:**
- Create: `internal/store/lock.go`, `internal/store/lock_test.go`
- Modify: `go.mod`, `go.sum` (adds `github.com/gofrs/flock v0.13.1`)

**Interfaces:**
- Produces: `store.ErrLockTimeout`; `store.Unlock func() error`; `store.Lock(ctx context.Context, path string, timeout time.Duration) (Unlock, error)`; `store.TryLock(path string) (Unlock, bool, error)` (non-blocking, for the flusher).

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/gofrs/flock@v0.13.1
go mod tidy
go tool -modfile=tools/go.mod govulncheck ./...
```

Expected: `go.mod` gains `github.com/gofrs/flock v0.13.1`; govulncheck reports no vulnerabilities (state the result in the commit body).

- [ ] **Step 2: Write the failing tests**

`internal/store/lock_test.go`:

```go
package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestLockSerialisesWriters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "state.lock")
	counterPath := filepath.Join(dir, "counter")
	if err := os.WriteFile(counterPath, []byte("0"), 0o600); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := Lock(context.Background(), lockPath, 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = unlock() }()
			raw, err := os.ReadFile(counterPath)
			if err != nil {
				errs <- err
				return
			}
			n, _ := strconv.Atoi(string(raw))
			time.Sleep(time.Millisecond)
			errs <- os.WriteFile(counterPath, []byte(strconv.Itoa(n+1)), 0o600)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("writer failed: %v", err)
		}
	}
	got, _ := os.ReadFile(counterPath)
	if string(got) != strconv.Itoa(writers) {
		t.Fatalf("counter %s, want %d (lost updates)", got, writers)
	}
}

func TestLockTimesOutWhileHeld(t *testing.T) {
	t.Parallel()
	lockPath := filepath.Join(t.TempDir(), "state.lock")
	unlock, err := Lock(context.Background(), lockPath, time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	defer func() { _ = unlock() }()
	_, err = Lock(context.Background(), lockPath, 50*time.Millisecond)
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("got %v, want ErrLockTimeout", err)
	}
}

func TestLockIsReleasedByUnlock(t *testing.T) {
	t.Parallel()
	lockPath := filepath.Join(t.TempDir(), "state.lock")
	unlock, err := Lock(context.Background(), lockPath, time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	second, err := Lock(context.Background(), lockPath, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("second lock after unlock: %v", err)
	}
	_ = second()
}

func TestTryLockReportsAHeldLockWithoutWaiting(t *testing.T) {
	t.Parallel()
	lockPath := filepath.Join(t.TempDir(), "flush.lock")
	unlock, held, err := TryLock(lockPath)
	if err != nil || !held {
		t.Fatalf("first TryLock: held=%v err=%v", held, err)
	}
	defer func() { _ = unlock() }()
	_, heldAgain, err := TryLock(lockPath)
	if err != nil || heldAgain {
		t.Fatalf("second TryLock: held=%v err=%v; want false, nil", heldAgain, err)
	}
}
```

- [ ] **Step 3: Run the tests and watch them fail**

Run: `go test ./internal/store/`
Expected: FAIL to build, `undefined: Lock`, `undefined: TryLock`, `undefined: ErrLockTimeout`.

- [ ] **Step 4: Write `internal/store/lock.go`**

```go
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/flock"
)

// ErrLockTimeout means the lock was still held when the wait budget ran out.
var ErrLockTimeout = errors.New("lock wait timed out")

// Unlock releases a lock obtained from Lock or TryLock.
type Unlock func() error

const lockRetryDelay = 10 * time.Millisecond

// Lock acquires the advisory file lock at path, waiting at most timeout.
func Lock(ctx context.Context, path string, timeout time.Duration) (Unlock, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lock := flock.New(path)
	locked, err := lock.TryLockContext(waitCtx, lockRetryDelay)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if !locked {
		return nil, fmt.Errorf("%w: %s", ErrLockTimeout, path)
	}
	return lock.Unlock, nil
}

// TryLock acquires the lock at path without waiting. held is false when another
// process holds it.
func TryLock(path string) (unlock Unlock, held bool, err error) {
	lock := flock.New(path)
	held, err = lock.TryLock()
	if err != nil {
		return nil, false, fmt.Errorf("try lock %s: %w", path, err)
	}
	if !held {
		return nil, false, nil
	}
	return lock.Unlock, true, nil
}
```

The public flock documentation says `TryLockContext` returns `false` when the context is cancelled or times out; whether it also returns the context error is not stated, so both outcomes are handled above.

- [ ] **Step 5: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/store/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/store/lock.go internal/store/lock_test.go
git commit -m "$(cat <<'EOF'
feat: add cross-platform advisory file locks

govulncheck: no vulnerabilities found after adding gofrs/flock v0.13.1.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Machine state on disk

**Files:**
- Create: `internal/store/state.go`, `internal/store/state_test.go`

**Interfaces:**
- Consumes: `quota.State`, `quota.NewState` (Task 1); `WriteFileAtomic` (Task 5).
- Produces: `store.StateFile` ("state.json"); `store.ErrStateCorrupt`; `store.FlushRecord{At time.Time, OK bool, Error string}`; `store.IngestionRecord{Current, History string, CurrentCapturedAt *time.Time, At time.Time}`; `store.State{quota.State; LastFlush *FlushRecord; LastIngestion map[string]IngestionRecord}`; `store.NewState() State`; `store.LoadState(home string) (State, error)`; `store.SaveState(home string, s State) error`.

- [ ] **Step 1: Write the failing tests**

`internal/store/state_test.go`:

```go
package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/quota"
)

func TestLoadStateReturnsAFreshStateWhenTheFileIsMissing(t *testing.T) {
	t.Parallel()
	got, err := LoadState(t.TempDir())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if diff := cmp.Diff(NewState(), got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestSaveStateThenLoadStateRoundTrips(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	used := 24.0
	reset := time.Date(2026, 9, 17, 16, 20, 0, 0, time.UTC)
	captured := time.Date(2026, 9, 17, 15, 30, 0, 0, time.UTC)
	want := NewState()
	want.Windows.FiveHour = quota.Window{Status: quota.WindowObserved, UsedPercentage: &used, ResetsAt: &reset}
	want.LastObservedAt = &captured
	want.ClaudeCodeVersion = "2.1.274"
	want.LastPublished = &quota.Published{EventID: "e1", CapturedAt: captured, Windows: want.Windows}
	want.LastFlush = &FlushRecord{At: captured, OK: true}
	want.LastIngestion = map[string]IngestionRecord{"databox-main": {Current: "ing-1", History: "ing-2", CurrentCapturedAt: &captured, At: captured}}
	if err := SaveState(home, want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, err := LoadState(home)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadStateReportsCorruptionAndStartsFresh(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, StateFile), []byte(`{"schemaVersion":1,"windows":{"fiveHo`), 0o600); err != nil {
		t.Fatalf("write truncated state: %v", err)
	}
	got, err := LoadState(home)
	if !errors.Is(err, ErrStateCorrupt) {
		t.Fatalf("got error %v, want ErrStateCorrupt", err)
	}
	if diff := cmp.Diff(NewState(), got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadStateIgnoresAStrayTemporaryFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := SaveState(home, NewState()); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".tmp-123"), []byte("garbage"), 0o600); err != nil {
		t.Fatalf("write stray file: %v", err)
	}
	got, err := LoadState(home)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if diff := cmp.Diff(NewState(), got); diff != "" {
		t.Fatalf("state mismatch (-want +got):\n%s", diff)
	}
}

func TestSaveStateWritesAnOwnerOnlyFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := SaveState(home, NewState()); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, StateFile))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("permissions %o, want no group or world bits", perm)
	}
}
```

On Windows the permission test is not meaningful; guard it with `if runtime.GOOS == "windows" { t.Skip("posix permissions") }` at the top of `TestSaveStateWritesAnOwnerOnlyFile` and add `"runtime"` to the imports.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/store/`
Expected: FAIL to build, `undefined: LoadState`, `undefined: SaveState`, `undefined: NewState`, `undefined: StateFile`, `undefined: ErrStateCorrupt`, `undefined: FlushRecord`, `undefined: IngestionRecord`.

- [ ] **Step 3: Write `internal/store/state.go`**

```go
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

// StateFile is the name of the machine state inside the home directory.
const StateFile = "state.json"

// ErrStateCorrupt means state.json exists but could not be decoded; the caller
// received a fresh state and should log the condition.
var ErrStateCorrupt = errors.New("state.json is not valid; starting from a fresh state")

// FlushRecord is the outcome of the most recent flusher run.
type FlushRecord struct {
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
	Error string    `json:"error"`
}

// IngestionRecord remembers what a sink last accepted, keyed by sink id.
type IngestionRecord struct {
	Current           string     `json:"current"`
	History           string     `json:"history"`
	CurrentCapturedAt *time.Time `json:"currentCapturedAt"`
	At                time.Time  `json:"at"`
}

// State is the persisted machine state: the quota state plus delivery bookkeeping.
type State struct {
	quota.State
	LastFlush     *FlushRecord               `json:"lastFlush,omitempty"`
	LastIngestion map[string]IngestionRecord `json:"lastIngestion,omitempty"`
}

// NewState returns the state of a machine that has never observed anything.
func NewState() State {
	return State{State: quota.NewState()}
}

// LoadState reads state.json. A missing file yields a fresh state and no error;
// an undecodable file yields a fresh state and ErrStateCorrupt.
func LoadState(home string) (State, error) {
	raw, err := os.ReadFile(filepath.Join(home, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return NewState(), fmt.Errorf("read %s: %w", StateFile, err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return NewState(), fmt.Errorf("%w: %w", ErrStateCorrupt, err)
	}
	return s, nil
}

// SaveState writes state.json atomically with owner-only permissions.
func SaveState(home string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	return WriteFileAtomic(filepath.Join(home, StateFile), data, 0o600)
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/store/` then `make check`.
Expected: `ok`, lint clean.

- [ ] **Step 5: Commit**

```bash
git add internal/store/state.go internal/store/state_test.go
git commit -m "$(cat <<'EOF'
feat: persist the machine state atomically

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Durable spool

**Files:**
- Create: `internal/store/spool.go`, `internal/store/spool_test.go`

**Interfaces:**
- Consumes: `quota.Snapshot`, `quota.EventType` (Tasks 2 and 3); `WriteFileAtomic`, `PendingDir`, `DeadLetterDir` (Task 5).
- Produces: `store.DeliveryState{Attempts int, NextAttemptAt time.Time, LastError, LastErrorCode string}`; `store.Event{EventType quota.EventType, Snapshot quota.Snapshot, Delivery map[string]DeliveryState, DeadLetteredAt *time.Time, Reason string}`; `store.PendingEvent{Path string, Event Event}`; `store.WritePending(home string, ev Event) (string, error)`; `store.ListPending(home string) ([]PendingEvent, error)`; `store.UpdatePending(pe PendingEvent) error`; `store.DeletePending(pe PendingEvent) error`; `store.DeadLetter(home string, pe PendingEvent, reason string, now time.Time) error`; `store.Requeue(home string, now time.Time) (int, error)`; `store.Counts(home string) (pending, dead int, err error)`.

- [ ] **Step 1: Write the failing tests**

`internal/store/spool_test.go`:

```go
package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/sulcer/gaugewire/internal/quota"
)

func spoolHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := EnsureLayout(home); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	return home
}

func event(id string, capturedAt time.Time) Event {
	return Event{
		EventType: quota.EventChange,
		Snapshot:  quota.Snapshot{SchemaVersion: 1, EventID: id, CapturedAt: capturedAt},
		Delivery:  map[string]DeliveryState{"databox-main": {NextAttemptAt: capturedAt}},
	}
}

func TestWritePendingNamesTheFileByCaptureTimeAndEventID(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 30, 0, 123_000_000, time.UTC)
	got, err := WritePending(home, event("evt-1", captured))
	if err != nil {
		t.Fatalf("WritePending: %v", err)
	}
	want := filepath.Join(home, PendingDir, "1789659000123-evt-1.json")
	if got != want {
		t.Fatalf("path %q, want %q", got, want)
	}
}

func TestListPendingReturnsEventsInCaptureOrder(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	base := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	later := event("evt-later", base.Add(time.Hour))
	earlier := event("evt-earlier", base)
	if _, err := WritePending(home, later); err != nil {
		t.Fatalf("write later: %v", err)
	}
	if _, err := WritePending(home, earlier); err != nil {
		t.Fatalf("write earlier: %v", err)
	}
	got, err := ListPending(home)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	want := []PendingEvent{
		{Path: filepath.Join(home, PendingDir, "1789657200000-evt-earlier.json"), Event: earlier},
		{Path: filepath.Join(home, PendingDir, "1789660800000-evt-later.json"), Event: later},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("pending mismatch (-want +got):\n%s", diff)
	}
}

func TestListPendingIgnoresTemporaryFiles(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	if err := os.WriteFile(filepath.Join(home, PendingDir, ".tmp-abc"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	got, err := ListPending(home)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %d events, %v; want 0, nil", len(got), err)
	}
}

func TestUpdatePendingRewritesDeliveryState(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	if _, err := WritePending(home, event("evt-1", captured)); err != nil {
		t.Fatalf("write: %v", err)
	}
	list, err := ListPending(home)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	pe := list[0]
	pe.Event.Delivery["databox-main"] = DeliveryState{Attempts: 1, NextAttemptAt: captured.Add(5 * time.Second), LastError: "timeout", LastErrorCode: ""}
	if err := UpdatePending(pe); err != nil {
		t.Fatalf("UpdatePending: %v", err)
	}
	again, err := ListPending(home)
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	if diff := cmp.Diff([]PendingEvent{pe}, again); diff != "" {
		t.Fatalf("pending mismatch (-want +got):\n%s", diff)
	}
}

func TestDeletePendingRemovesTheFile(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	if _, err := WritePending(home, event("evt-1", time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC))); err != nil {
		t.Fatalf("write: %v", err)
	}
	list, _ := ListPending(home)
	if err := DeletePending(list[0]); err != nil {
		t.Fatalf("DeletePending: %v", err)
	}
	pending, dead, err := Counts(home)
	if err != nil || pending != 0 || dead != 0 {
		t.Fatalf("counts %d/%d, %v; want 0/0, nil", pending, dead, err)
	}
}

func TestDeadLetterMovesTheEventWithAReason(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	now := captured.Add(time.Minute)
	if _, err := WritePending(home, event("evt-1", captured)); err != nil {
		t.Fatalf("write: %v", err)
	}
	list, _ := ListPending(home)
	if err := DeadLetter(home, list[0], "http 401 invalid_api_key", now); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}
	pending, dead, err := Counts(home)
	if err != nil || pending != 0 || dead != 1 {
		t.Fatalf("counts %d/%d, %v; want 0/1, nil", pending, dead, err)
	}
}

func TestRequeueMovesDeadLettersBackWithFreshDelivery(t *testing.T) {
	t.Parallel()
	home := spoolHome(t)
	captured := time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	ev := event("evt-1", captured)
	ev.Delivery["databox-main"] = DeliveryState{Attempts: 3, NextAttemptAt: captured, LastError: "boom", LastErrorCode: "forbidden"}
	if _, err := WritePending(home, ev); err != nil {
		t.Fatalf("write: %v", err)
	}
	list, _ := ListPending(home)
	if err := DeadLetter(home, list[0], "http 403 forbidden", captured.Add(time.Minute)); err != nil {
		t.Fatalf("DeadLetter: %v", err)
	}
	now := captured.Add(time.Hour)
	n, err := Requeue(home, now)
	if err != nil || n != 1 {
		t.Fatalf("Requeue: n=%d err=%v; want 1, nil", n, err)
	}
	got, err := ListPending(home)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	wantEvent := event("evt-1", captured)
	wantEvent.Delivery["databox-main"] = DeliveryState{NextAttemptAt: now}
	want := []PendingEvent{{Path: filepath.Join(home, PendingDir, "1789657200000-evt-1.json"), Event: wantEvent}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("pending mismatch (-want +got):\n%s", diff)
	}
}
```

The unix-millisecond prefixes above follow from the fixed capture times: `2026-09-17T15:30:00.123Z` is `1789659000123`, `2026-09-17T15:00:00Z` is `1789657200000`, `2026-09-17T16:00:00Z` is `1789660800000`. If a test disagrees, recompute the epoch with `date -u -d 2026-09-17T15:00:00Z +%s` (GNU) or `date -j -u -f %Y-%m-%dT%H:%M:%SZ 2026-09-17T15:00:00Z +%s` (macOS) and correct the plan's number, never the implementation.

- [ ] **Step 2: Run the tests and watch them fail**

Run: `go test ./internal/store/`
Expected: FAIL to build, `undefined: Event`, `undefined: DeliveryState`, `undefined: WritePending`, `undefined: ListPending`, `undefined: UpdatePending`, `undefined: DeletePending`, `undefined: DeadLetter`, `undefined: Requeue`, `undefined: Counts`, `undefined: PendingEvent`.

- [ ] **Step 3: Write `internal/store/spool.go`**

```go
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sulcer/gaugewire/internal/quota"
)

// DeliveryState tracks one sink's attempts to deliver an event.
type DeliveryState struct {
	Attempts      int       `json:"attempts"`
	NextAttemptAt time.Time `json:"nextAttemptAt"`
	LastError     string    `json:"lastError"`
	LastErrorCode string    `json:"lastErrorCode"`
}

// Event is a spooled snapshot with its delivery bookkeeping. Delivery is keyed
// by the sink ids enabled when the event was captured.
type Event struct {
	EventType      quota.EventType          `json:"eventType"`
	Snapshot       quota.Snapshot           `json:"snapshot"`
	Delivery       map[string]DeliveryState `json:"delivery"`
	DeadLetteredAt *time.Time               `json:"deadLetteredAt,omitempty"`
	Reason         string                   `json:"reason,omitempty"`
}

// PendingEvent is an event together with the file that holds it.
type PendingEvent struct {
	Path  string
	Event Event
}

// WritePending stores a new event in pending/ and returns its path. The name
// sorts chronologically: <captured unix millis>-<event id>.json.
func WritePending(home string, ev Event) (string, error) {
	name := fmt.Sprintf("%d-%s.json", ev.Snapshot.CapturedAt.UnixMilli(), ev.Snapshot.EventID)
	path := filepath.Join(home, PendingDir, name)
	if err := writeEvent(path, ev); err != nil {
		return "", err
	}
	return path, nil
}

// ListPending returns every pending event in chronological order. A file that
// cannot be decoded fails the listing so the caller can report it.
func ListPending(home string) ([]PendingEvent, error) {
	return listEvents(filepath.Join(home, PendingDir))
}

// UpdatePending rewrites an event in place, atomically.
func UpdatePending(pe PendingEvent) error {
	return writeEvent(pe.Path, pe.Event)
}

// DeletePending removes a delivered event.
func DeletePending(pe PendingEvent) error {
	if err := os.Remove(pe.Path); err != nil {
		return fmt.Errorf("delete %s: %w", pe.Path, err)
	}
	return nil
}

// DeadLetter moves an event whose delivery failed permanently into dead-letter/,
// recording when and why. It is never deleted automatically.
func DeadLetter(home string, pe PendingEvent, reason string, now time.Time) error {
	ev := pe.Event
	at := now.UTC()
	ev.DeadLetteredAt = &at
	ev.Reason = reason
	target := filepath.Join(home, DeadLetterDir, filepath.Base(pe.Path))
	if err := writeEvent(target, ev); err != nil {
		return err
	}
	return DeletePending(pe)
}

// Requeue moves every dead-letter event back to pending/ with its delivery state
// reset, so the next flush retries it. It returns how many events were moved.
func Requeue(home string, now time.Time) (int, error) {
	dead, err := listEvents(filepath.Join(home, DeadLetterDir))
	if err != nil {
		return 0, err
	}
	for _, pe := range dead {
		ev := pe.Event
		ev.DeadLetteredAt = nil
		ev.Reason = ""
		for sink := range ev.Delivery {
			ev.Delivery[sink] = DeliveryState{NextAttemptAt: now.UTC()}
		}
		if err := writeEvent(filepath.Join(home, PendingDir, filepath.Base(pe.Path)), ev); err != nil {
			return 0, err
		}
		if err := os.Remove(pe.Path); err != nil {
			return 0, fmt.Errorf("remove %s: %w", pe.Path, err)
		}
	}
	return len(dead), nil
}

// Counts reports how many events wait in pending/ and dead-letter/.
func Counts(home string) (pending, dead int, err error) {
	pending, err = countEvents(filepath.Join(home, PendingDir))
	if err != nil {
		return 0, 0, err
	}
	dead, err = countEvents(filepath.Join(home, DeadLetterDir))
	if err != nil {
		return 0, 0, err
	}
	return pending, dead, nil
}

func writeEvent(path string, ev Event) error {
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	return WriteFileAtomic(path, data, 0o600)
}

func eventFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func listEvents(dir string) ([]PendingEvent, error) {
	names, err := eventFiles(dir)
	if err != nil {
		return nil, err
	}
	out := make([]PendingEvent, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		out = append(out, PendingEvent{Path: path, Event: ev})
	}
	return out, nil
}

func countEvents(dir string) (int, error) {
	names, err := eventFiles(dir)
	if err != nil {
		return 0, err
	}
	return len(names), nil
}
```

- [ ] **Step 4: Run the tests and watch them pass**

Run: `go test -race -count=1 ./internal/store/` then `make check` then `make cover`.
Expected: `ok`, lint clean, the gate still passes for the two pure packages, and the total coverage line is printed.

- [ ] **Step 5: Commit**

```bash
git add internal/store/spool.go internal/store/spool_test.go
git commit -m "$(cat <<'EOF'
feat: add the durable event spool with dead letters and requeue

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Flip the spec markers and close the plan

**Files:**
- Modify: `docs/spec/gaugewire/data-contract.md:3`, `docs/spec/gaugewire/reducer-and-dedupe.md:3`, `docs/spec/gaugewire/spool-and-flush.md:3`, `docs/spec/gaugewire/testing-strategy.md:3`, `docs/spec/gaugewire/README.md:3`, `docs/spec/README.md`
- Delete: `docs/plans/2026-09-17-domain-and-store.md`

- [ ] **Step 1: Flip the markers of what this plan built**

Each page's line 3 is `Status: Draft · Planned · 2026-09-17 · <sentence>`. Change the build state only:

- `data-contract.md`: `Built` (the snapshot, state and event shapes exist and are tested).
- `reducer-and-dedupe.md`: `Built`.
- `spool-and-flush.md`: `Partial` and add one sentence at the end of its "At a glance" paragraph: "The spool, locks and dead-letter handling are built; the flusher process is not."
- `testing-strategy.md`: `Partial` and add after the unit matrix table: "Built so far: the `quota`, `source/claude` and `store` rows."
- `README.md` (gaugewire): `Partial`.
- `docs/spec/README.md`: the `gaugewire` row becomes `Draft | Partial`.

Read each changed page against the tree before editing; a build state is a claim about the tree.

- [ ] **Step 2: Delete this plan**

```bash
git rm docs/plans/2026-09-17-domain-and-store.md
```

- [ ] **Step 3: Verify links and commit**

Run the link check over `docs/spec` (every `](...md)` target must exist relative to its file) and `make check`.

```bash
git add docs/spec
git commit -m "$(cat <<'EOF'
docs: mark the domain, parser and store as built

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
)"
```

Then, with the owner's approval: push the branch, open the pull request with `gh pr create --assignee sulcer --label patch` and the Summary/Test plan body, and let CI run.
