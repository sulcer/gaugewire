# Claude Quota Observer — Implementation Specification v1.0

## 1. Mission

Build a small cross-platform application named **Claude Quota Observer**, abbreviated `cqo`.

Its sole purpose is to collect Claude Code subscription quota information from Claude Code's official `statusLine` JSON interface, normalize it, persist it reliably, and publish it to one or more pluggable observability destinations.

For v1:

```text
Source      = Claude Code statusLine
Destination = Databox API v1
Platforms   = macOS, Linux, Windows
Language    = Go
```

The product is **observability only**.

It must never:

```text
call undocumented Anthropic APIs
read Claude OAuth/session credentials
scrape /usage
scrape terminal output
depend on Herdr/tmux
switch Claude accounts
rotate accounts
proxy Claude requests
monitor prompts/responses/transcripts
run a permanent daemon
```

The design invariant is:

```text
Claude Code
    ↓
official statusLine JSON
    ↓
Normalize
    ↓
QuotaSnapshot v1
    ↓
Persist
    ↓
Sink Router
    ↓
Databox today / other sinks later
```

---

## 2. Core architecture

```text
                   WORKER MACHINE

                  Official Claude Code
                         │
                         │ statusLine JSON
                         ▼
              ┌──────────────────────┐
              │         CQO          │
              │                      │
              │ StatusLineSource     │
              │       ↓              │
              │ Validate             │
              │       ↓              │
              │ State Reducer        │
              │       ↓              │
              │ QuotaSnapshot v1     │
              │       ↓              │
              │ Dedupe               │
              │       ↓              │
              │ Durable Spool        │
              │       ↓              │
              │ Sink Router          │
              └──────────┬───────────┘
                         │
                  outbound HTTPS
                         │
          ┌──────────────┼──────────────┐
          ▼              ▼              ▼
       Databox          OTLP         Webhook
         v1            future         future
```

Herdr, tmux, Terminal.app, Windows Terminal, SSH, etc. are outside this architecture.

CQO operates identically regardless of how Claude Code is hosted.

---

## 3. Fleet invariant

For v1:

> One CQO installation / OS user corresponds to exactly one Claude subscription identity.

Multiple Claude Code sessions may run simultaneously on the machine.

Example:

```text
Mac Mini 01
Claude subscription A

Herdr
├─ Claude session 1
├─ Claude session 2
├─ Claude session 3
└─ Claude session 4

          ↓

ONE machine-level CQO state
          ↓
Claude account A
```

Do not infer the Claude account from OAuth files, cookies, email addresses, or credentials.

Identity is explicitly configured:

```yaml
node:
  id: "generated-uuid"
  alias: "mac-mini-01"

account:
  id: "configured-stable-uuid"
  alias: "claude-01"
```

If the user changes which Claude account is logged into that machine, they must explicitly rebind/update the CQO account identity.

CQO must never claim to have cryptographically verified the Claude account.

---

## 4. Claude Code source contract

CQO consumes JSON from stdin when invoked as Claude Code's `statusLine.command`.

Only read:

```text
version

rate_limits.five_hour.used_percentage
rate_limits.five_hour.resets_at

rate_limits.seven_day.used_percentage
rate_limits.seven_day.resets_at
```

Everything else must be ignored for observability purposes.

Do not persist:

```text
session_id
prompt_id
cwd
repository
transcript_path
model conversation
prompts
responses
source code
```

Example relevant source input:

```json
{
  "version": "2.1.274",
  "rate_limits": {
    "five_hour": {
      "used_percentage": 24,
      "resets_at": 1789659600
    },
    "seven_day": {
      "used_percentage": 53,
      "resets_at": 1789714800
    }
  }
}
```

CQO must require Claude Code >= `2.1.251`.

`doctor` must report a clear error for older versions.

---

## 5. Missing-value semantics

This is critical.

Never interpret an absent quota as zero.

```text
missing != 0%
```

The internal state for each quota window is:

```go
type WindowStatus string

const (
    WindowUnknown  WindowStatus = "unknown"
    WindowObserved WindowStatus = "observed"
    WindowExpired  WindowStatus = "expired"
)
```

Each window contains:

```go
type QuotaWindow struct {
    Status         WindowStatus
    UsedPercentage *float64
    ResetsAt       *time.Time
}
```

Reducer rules:

### Incoming window exists

Validate:

```text
0 <= used_percentage <= 100
resets_at is valid Unix epoch seconds
```

Then set:

```text
status = observed
usedPercentage = incoming value
resetsAt = incoming reset
```

### Incoming window missing, no previous observation

Set:

```text
status = unknown
usedPercentage = null
resetsAt = null
```

Do not publish an initial meaningless `unknown/unknown` event.

### Incoming window missing, previous reset is still in the future

Preserve previous state.

This covers a new Claude session before its first API response.

Example:

```text
previous:
5h = 42%, resets 18:00

new session starts at 15:00
rate_limits absent

result:
5h remains 42%, resets 18:00
```

### Incoming window missing and previous reset time has passed

Set:

```text
status = expired
usedPercentage = null
```

Retain the previous reset timestamp for diagnostics.

Do not invent `0%`.

A later valid Claude response moves it back to `observed`.

---

## 6. Stable normalized contract

Do not expose Anthropic's raw schema to sinks.

Define our own versioned domain object:

```json
{
  "schemaVersion": 1,

  "eventId": "UUID",

  "node": {
    "id": "node-uuid",
    "alias": "mac-mini-01",
    "platform": "darwin"
  },

  "account": {
    "id": "account-uuid",
    "alias": "claude-01"
  },

  "capturedAt": "2026-09-17T15:30:00Z",

  "windows": {
    "fiveHour": {
      "status": "observed",
      "usedPercentage": 24,
      "resetsAt": "2026-09-17T16:20:00Z"
    },

    "sevenDay": {
      "status": "observed",
      "usedPercentage": 53,
      "resetsAt": "2026-09-18T07:00:00Z"
    }
  },

  "source": {
    "type": "claude-code-statusline",
    "claudeCodeVersion": "2.1.274"
  },

  "observerVersion": "1.0.0"
}
```

Future Anthropic schema changes should affect only the source adapter.

Future destination changes should affect only sink adapters.

---

## 7. Status-line integration

There can only be one effective status-line command, and the user may already have a custom status line.

CQO must therefore act as a transparent adapter.

Before:

```text
Claude
   ↓
existing statusline renderer
   ↓
terminal
```

After:

```text
                   raw stdin JSON
                        │
               ┌────────┴────────┐
               ▼                 ▼
        CQO observability     existing renderer
               │                 │
               ▼                 ▼
          local state       existing stdout
               │                 │
               ▼                 ▼
             sinks            terminal
```

CQO must pass the **exact original JSON bytes** to the existing renderer.

Observability must not modify the renderer input.

The existing rendered output must be forwarded unchanged.

Observability errors must be fail-open:

```text
CQO collection fails
        ↓
existing status line still works
```

The observer must never intentionally blank or replace the user's current status-line appearance.

---

## 8. Installation behavior

Implement:

```bash
cqo install
cqo uninstall
cqo doctor
cqo status
cqo flush
```

### `cqo install`

Must:

1. locate `~/.claude/settings.json`;
2. inspect current `statusLine`;
3. make a timestamped backup;
4. preserve the complete original `statusLine` object;
5. save the original renderer command to CQO configuration;
6. replace only the `statusLine.command` with the CQO status-line adapter command;
7. preserve fields such as:
   - `padding`
   - `refreshInterval`
   - `hideVimModeIndicator`
8. generate a stable node UUID if one doesn't exist;
9. configure node/account aliases;
10. configure sink(s);
11. never overwrite an existing CQO installation without confirmation/explicit force.

Do **not** automatically remove the user's existing `refreshInterval`.

CQO must tolerate even a 1-second refresh interval through deduplication.

`doctor` may recommend removing an unnecessary aggressive interval, but installation must not change it.

### `cqo uninstall`

If the current status-line command still points to CQO:

```text
restore exact original statusLine object
```

If the user has manually changed their status-line configuration after installation:

```text
DO NOT overwrite it
warn the user
```

Never overwrite unrelated Claude settings.

---

## 9. Runtime lifecycle

There must be no permanent CQO daemon.

The normal hot path is:

```text
Claude invokes statusLine
        ↓
CQO starts
        ↓
read stdin
        ↓
start/preserve renderer
        ↓
parse quota
        ↓
machine lock
        ↓
reduce state
        ↓
dedupe decision
        ↓
atomic state/spool write if needed
        ↓
release lock
        ↓
trigger short-lived flusher if needed
        ↓
forward renderer stdout
        ↓
CQO exits
```

When Claude is idle:

```text
CQO processes running = 0
CQO CPU usage = 0
```

Long-lived Claude/Herdr sessions therefore have no persistent CQO cost.

---

## 10. Concurrency

Assume many Claude Code sessions can invoke CQO at nearly the same time.

Implement real cross-platform file locking.

Do not use:

```text
PID-file existence
sleep/retry hacks
"if file exists"
```

Maintain at least:

```text
state.lock
flush.lock
```

`state.lock` protects:

```text
state reduction
deduplication
spool creation
state persistence
```

Never hold `state.lock` while performing network I/O.

`flush.lock` guarantees only one publisher/flusher operates per machine.

---

## 11. Local state

Persist machine-wide state.

Suggested structure:

```text
<CQO_DATA_DIR>/
├── config.yaml
├── state.json
├── state.lock
├── flush.lock
├── pending/
│   ├── event-a.json
│   └── event-b.json
├── dead-letter/
└── logs/
```

Use platform-appropriate durable user-data/config directories.

All writes must use:

```text
write temporary file
fsync where appropriate
atomic rename
```

A crash must not corrupt `state.json`.

Do not require SQLite, Redis, or another database.

---

## 12. Deduplication

Status-line execution frequency must not equal Databox ingestion frequency.

Track `lastPublishedSnapshot`.

Publish when any of these are true:

```text
first valid quota observation

window status changes

reset timestamp changes

5h usage changes >= configured threshold

7d usage changes >= configured threshold

heartbeat interval elapsed while status-line activity is occurring
```

Defaults:

```yaml
publishing:
  minDeltaPercentage: 1.0
  heartbeatInterval: 30m
```

Compare percentage changes to the **last published value**, not merely the immediately previous input.

Example:

```text
last published = 40.0

40.2 → ignore
40.4 → ignore
40.7 → ignore
41.1 → publish
```

Publish the actual `41.1`, not a rounded value.

---

## 13. Durable spool

The status-line hot path must persist before relying on network delivery.

```text
QuotaSnapshot
      ↓
atomic pending event
      ↓
safe locally
      ↓
publisher
```

If Databox is unavailable, the event remains locally.

Never silently discard a quota observation because of:

```text
network outage
Databox outage
process cancellation
rate limiting
machine sleep
temporary DNS failure
```

Spool events must retain the list of sink IDs they target.

Adding a new sink later must not retroactively send all historic pending events to it unless explicitly requested.

---

## 14. Publishing process

Do not perform slow network work synchronously on the status-line critical path.

After creating a pending event, CQO should start a **detached, short-lived**:

```bash
cqo flush
```

process if no flusher is already active.

The flusher:

```text
starts
 ↓
takes flush.lock
 ↓
reads due pending records
 ↓
publishes
 ↓
updates acknowledgements
 ↓
retries/dead-letters as appropriate
 ↓
exits
```

It is not a daemon.

It should normally live for seconds at most.

Implement platform-specific detached-process handling for:

```text
macOS
Linux
Windows
```

If launching the flusher fails, leave the spool intact.

Any later status-line invocation should notice due pending work and attempt to launch a flusher again.

---

## 15. Retry strategy

Persist retry state.

Suggested retry schedule:

```text
5 seconds
30 seconds
2 minutes
10 minutes
30 minutes
30 minutes...
```

Add jitter.

Classify errors.

Retry:

```text
network errors
timeouts
429/rate_limited
5xx
service_unavailable
processing_failed where retry is reasonable
```

Block/dead-letter:

```text
invalid credentials
forbidden
schema mismatch
invalid input
permanent configuration errors
```

Never delete failed events.

`cqo status` and `cqo doctor` must clearly surface blocked/dead-letter delivery.

---

## 16. Sink abstraction

Core domain code must have no Databox dependencies.

Use an interface conceptually equivalent to:

```go
type Sink interface {
    ID() string

    PublishBatch(
        ctx context.Context,
        snapshots []QuotaSnapshot,
    ) error
}
```

Implement:

```text
sink/
├── sink.go
├── router.go
└── databox/
```

Future:

```text
otlp/
prometheus/
webhook/
influxdb/
file/
```

Config:

```yaml
sinks:
  - id: databox-main
    type: databox
    enabled: true
```

The router must support more than one configured sink even though v1 ships only Databox.

---

## 17. Databox sink

Use **Databox REST API v1**.

Authentication:

```text
x-api-key
```

Never use the deprecated v0 Push API.

Never use MCP for deterministic CQO ingestion.

MCP can later be used independently for conversational analysis.

---

## 18. Databox datasets

Use one data source:

```text
Claude Quota Observer
```

and two datasets.

### Dataset A — Claude Quota Current

Purpose:

```text
current fleet capacity dashboard
```

Primary key:

```text
account_id
```

Columns:

```text
account_id
account_alias

node_id
node_alias
platform

latest_event_id
captured_at
last_seen_at

five_hour_status
five_hour_used_percentage
five_hour_resets_at

seven_day_status
seven_day_used_percentage
seven_day_resets_at

claude_code_version
observer_version
```

One row per Claude subscription.

### Dataset B — Claude Quota History

Purpose:

```text
historical trends
consumption velocity
audit/history
```

Primary key:

```text
event_id
```

Columns:

```text
event_id
event_type

account_id
account_alias

node_id
node_alias
platform

captured_at
published_at

five_hour_status
five_hour_used_percentage
five_hour_resets_at

seven_day_status
seven_day_used_percentage
seven_day_resets_at

source_type
claude_code_version
observer_version
```

`event_type`:

```text
change
state_transition
heartbeat
```

---

## 19. Databox delivery ordering

Because `Current` represents latest state, do not allow an old retry to overwrite newer state.

Each machine has one flusher.

Process events chronologically.

For a flush batch:

```text
pending:
E1
E2
E3

History:
write E1, E2, E3

Current:
write only E3
```

History uses `event_id`, making retry idempotent.

Current uses `account_id`.

Do not acknowledge/delete pending events until the sink's required writes have succeeded.

---

## 20. Databox bootstrap

Implement an optional:

```bash
cqo databox bootstrap
```

It should:

```text
validate API key
select/configure Databox account
create or reuse data source
create Current dataset
create History dataset
persist dataset IDs
perform a test ingestion only with explicit confirmation
```

Do not automatically create duplicate resources on repeated runs.

Also support manually supplied existing IDs:

```yaml
sinks:
  - id: databox-main
    type: databox

    currentDatasetId: "..."
    historyDatasetId: "..."
```

---

## 21. Secrets

Never store API credentials inside:

```text
QuotaSnapshot
state.json
event files
Claude settings.json
git repositories
logs
```

Support:

```yaml
credentials:
  apiKeyEnv: DATABOX_API_KEY
```

and optionally:

```yaml
credentials:
  apiKeyFile: /secure/path/databox.key
```

File permissions on Unix:

```text
directories 0700
secret files 0600
```

Use user-scoped security on Windows.

Never log the key.

---

## 22. Renderer behavior

If an existing status-line renderer is configured, CQO must invoke it with the exact original stdin.

Observability and rendering should be isolated.

Renderer errors must be reported separately from observer errors.

CQO should prioritize preserving existing terminal behavior.

Do not automatically append quota information to the visible status line.

The user may separately customize their status line to show:

```text
5h 24% | 7d 53%
```

but that is not CQO's responsibility.

---

## 23. Logging

Never log raw status-line JSON.

Log only sanitized information such as:

```text
timestamp
event ID
quota parse success/failure
sink ID
delivery status
retry count
error code
observer version
```

No:

```text
session IDs
paths
repositories
transcripts
prompts
Claude credentials
Databox secrets
```

Use bounded log rotation.

---

## 24. CLI

Minimum CLI:

```text
cqo install
cqo uninstall

cqo statusline
cqo flush

cqo status
cqo doctor

cqo databox bootstrap

cqo version
```

### `status`

Example:

```text
Claude Quota Observer

Node:     mac-mini-01
Account:  claude-01

5h:       24%
Reset:    18:20

7d:       53%
Reset:    Sep 18 09:00

Last observation:  2m ago
Last publish:      2m ago

Pending events:    0
Dead letters:      0

Databox:           healthy
```

### `doctor`

Check:

```text
CQO configuration
Claude Code version
statusLine integration
renderer configuration
node/account identity
filesystem permissions
state readability
spool writability
Databox API key validity
dataset configuration
pending/dead-letter health
last successful quota observation
```

Example:

```text
Claude Code       2.1.274        ✓
StatusLine source                ✓
5h quota          observed       ✓
7d quota          observed       ✓
Renderer                         ✓
Local state                      ✓
Databox auth                     ✓
Current dataset                  ✓
History dataset                  ✓
Pending          0               ✓

HEALTHY
```

---

## 25. Repository layout

Use approximately:

```text
claude-quota-observer/
├── cmd/
│   └── cqo/
│       └── main.go
│
├── internal/
│   ├── source/
│   │   └── statusline/
│   │
│   ├── domain/
│   │   ├── snapshot.go
│   │   ├── window.go
│   │   └── reducer.go
│   │
│   ├── dedupe/
│   │
│   ├── state/
│   │
│   ├── spool/
│   │
│   ├── renderer/
│   │
│   ├── sink/
│   │   ├── sink.go
│   │   ├── router.go
│   │   └── databox/
│   │
│   ├── config/
│   ├── install/
│   ├── doctor/
│   ├── lock/
│   └── logging/
│
├── fixtures/
│   └── statusline/
│
├── tests/
│
├── docs/
│   ├── architecture.md
│   └── databox.md
│
├── config.example.yaml
├── go.mod
└── README.md
```

Keep packages small and dependency direction clean:

```text
source → domain

domain → nothing infrastructure-specific

dedupe/state/spool → domain

sink → domain

databox → sink/domain

CLI → application components
```

---

## 26. Tests

Unit-test at minimum:

```text
rate_limits completely absent

5h present, 7d absent

7d present, 5h absent

both present

0% is preserved as 0

100% is preserved

decimal percentage

malformed percentage

malformed reset timestamp

new session missing rate_limits preserves unexpired machine state

reset passes + missing window => expired

expired + fresh observation => observed

multi-session duplicate events

percentage threshold accumulation

reset timestamp change triggers event

heartbeat generation

concurrent statusLine invocations

atomic state recovery

spool retry after network outage

sink idempotency

Databox 401

Databox 429

Databox 5xx

Databox structured processing error

renderer stdin byte-for-byte passthrough

renderer stdout passthrough

observer error does not break renderer

install/uninstall restoration
```

Use `httptest` for Databox tests.

No unit test should contact the real Databox service.

---

## 27. Real acceptance test

Before calling v1 complete, perform this on a real Claude Pro/Max machine.

### Source

Start Claude.

Before first model response:

```text
CQO must not record 0/0.
```

After a response:

```text
CQO receives 5h + 7d.
```

Compare with `/usage`.

They should represent the same quota state.

### Existing status line

Before installation:

```text
capture current visible status line.
```

After installation:

```text
must remain functionally identical.
```

### Multiple sessions

Open several simultaneous Claude sessions.

Expected:

```text
one machine state
no duplicate flood
```

### Network failure

Disconnect Databox/network.

Cause quota state change.

Expected:

```text
event remains pending locally.
```

Reconnect.

Expected:

```text
pending event eventually publishes.
```

### Databox

Verify:

```text
Current = exactly one account row

History = chronological unique events
```

### Uninstall

Run:

```bash
cqo uninstall
```

Expected:

```text
original Claude statusLine restored exactly
CQO removed from statusLine path
historical CQO data may remain unless explicitly deleted
```

---

## 28. Non-functional requirements

Target:

```text
no permanent daemon

no network request on the statusLine critical path

local statusLine processing:
p95 < 50ms excluding existing renderer

idle CPU:
0

idle CQO processes:
0

network:
outbound HTTPS only

delivery:
at least once

Databox history:
idempotent via event_id

Databox current:
idempotent via account_id

cross-platform:
darwin arm64/amd64
linux arm64/amd64
windows amd64
```

Prefer a single Go binary with `CGO_ENABLED=0` where practical.

---

## 29. Security/privacy invariants

Treat these as hard requirements.

CQO must:

```text
collect quota only

never read Claude credentials

never send anything to Anthropic

never read transcript files

never inspect prompts

never inspect source code

never inspect working-directory contents

never send raw Claude statusLine payloads externally

never store Databox keys in event data

never infer user identity from Claude auth state
```

Only normalized quota metadata leaves the machine.

---

## 30. v1 explicitly out of scope

Do not implement:

```text
automatic account switching

account rotation

Claude OAuth access

private Anthropic endpoints

/usage scraping

terminal scraping

Herdr plugins

MCP ingestion

Grafana integration

Slack alerts

central remote control

fleet orchestration

per-model quota tracking

account credential discovery

prompt/token analytics

Claude conversation telemetry
```

Do not add these opportunistically.

---

## 31. Future extensibility

The architecture must allow these without modifying core quota logic:

```text
Databox → Grafana/OTLP
Databox → Prometheus
Databox → InfluxDB
Databox → generic webhook
Databox → file

or

Databox + OTLP simultaneously
```

The stable boundary is:

```text
QuotaSnapshot v1
```

Source adapters and sink adapters are replaceable.

Potential future source:

```text
source/statusline    ← v1 official source
source/other         ← only if ever needed
```

Potential future sinks:

```text
sink/databox
sink/otlp
sink/webhook
sink/prometheus
```

No sink is allowed to leak into the domain layer.

---

## 32. Implementation order

Implement in this order:

```text
1. domain model + reducer
2. official statusLine parser
3. machine state
4. deduplication
5. atomic file persistence
6. concurrency/file locking
7. durable spool
8. renderer passthrough
9. statusLine CLI mode
10. sink interface/router
11. Databox API client
12. detached flusher/retries
13. doctor/status commands
14. install/uninstall
15. Databox bootstrap
16. cross-platform packaging
17. real Claude integration test
18. multi-machine rollout
```

Do not begin with Databox.

Prove this first:

```text
real Claude statusLine
       ↓
correct QuotaSnapshot
       ↓
correct durable local state
```

Only then add transport.

---

## 33. Definition of Done

v1 is complete only when all of these are true:

```text
✓ official Claude statusLine is the only Claude quota source

✓ real Pro/Max 5h and 7d values captured correctly

✓ missing quota never becomes 0

✓ existing status line preserved

✓ concurrent Claude sessions do not create duplicate floods

✓ no permanent CQO process exists

✓ network outage cannot lose a captured event

✓ Databox Current contains latest state per account

✓ Databox History contains idempotent historical events

✓ API credentials never appear in state/events/logs

✓ install is reversible

✓ macOS works

✓ Linux works

✓ Windows works

✓ sink interface is destination-independent

✓ Databox can be replaced without changing source/domain code

✓ test suite passes

✓ doctor reports healthy on a real installed machine
```

---

## Architecture decision to preserve

Put this near the top of the repository's `ARCHITECTURE.md`:

> **Claude Code owns quota discovery. Claude Quota Observer owns normalization, local durability, and delivery. Sinks own storage and visualization.**

And:

> **Capture → Normalize → Persist → Publish.**

Not:

> **Capture → Databox.**

That distinction is what keeps this from becoming a Databox-specific script.

---

## Final implementation note

Databox API ingestion itself can be event-driven/immediate, but downstream metric/dashboard refresh may still follow the data source's configured sync frequency.

Do not assume that API acceptance means instantaneous visualization.

Measure actual dashboard latency during acceptance testing and document the observed behavior.
