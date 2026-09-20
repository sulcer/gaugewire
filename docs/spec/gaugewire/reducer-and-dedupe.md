# Reducer and dedupe

Status: Draft · Built · 2026-09-20 · How one observation becomes machine state, and when state becomes an event.

## At a glance

Each window is reduced independently through a three-state machine. A missing window is never
zero. Every open Claude Code session runs the status line with the rate limits it last received,
so one machine sees many readings of one window, and after a schedule change readings of a
window that no longer applies. A window is identified by its reset time; the window open now is
the one resetting soonest among those still ahead, and within it usage only rises. Publishing
is decided against the last published snapshot, not the previous input, so small drifts
accumulate until they cross the threshold.

## Diagram

```mermaid
stateDiagram-v2
    [*] --> unknown
    unknown --> observed: reading with resetsAt > now
    observed --> observed: sooner open resetsAt, or same resetsAt and used ≥ stored
    observed --> observed: reading refused or absent, stored resetsAt > now (keep)
    observed --> expired: reading refused or absent, stored resetsAt ≤ now
    expired --> observed: reading with resetsAt > now
    unknown --> unknown: absent, or every reading already reset
```

## Validation

An incoming window is valid when `0 ≤ used_percentage ≤ 100` and `resets_at` is a positive
integer of epoch seconds. An invalid window is treated as absent for that invocation; the parser
reports the failing window path (`rate_limits.five_hour` or `rate_limits.seven_day`) to its
caller, and the hot path (not yet built) logs it once. The
`version` must parse and be at least `2.1.251`; otherwise the whole observation is skipped and
rendering continues.

## Reducer rules, per window

| Incoming | Stored | Result |
|---|---|---|
| present, `resetsAt ≤ now` | any | ignore: that window has ended |
| present, open | not `observed` | accept: `observed`, copy value and reset |
| present, open | `observed`, stored `resetsAt ≤ now` | accept: the stored window has ended |
| present, open | `observed`, same `resetsAt`, incoming `used ≥ stored` | accept |
| present, open | `observed`, same `resetsAt`, incoming `used < stored` | ignore (a session behind the others) |
| present, open | `observed`, incoming `resetsAt` sooner | accept: the current window |
| present, open | `observed`, incoming `resetsAt` later | ignore: a superseded schedule |
| absent, or ignored | `unknown` | stay `unknown` |
| absent, or ignored | stored `resetsAt > now` | keep stored |
| absent, or ignored | stored `resetsAt ≤ now` | `expired`, `usedPercentage = null`, keep `resetsAt` |

**Why the guard exists.** Measured on a machine with eight open sessions, each ticking every
five seconds: seven-day readings of 27, 44, 64, 66, 76 and 78 per cent all carried the same
reset, one per session's cache, and the highest matched what Claude Code's `/usage` reported.
Two further sessions carried a different reset, four days later, at 7 and 1 per cent: the
schedule of the weekly window had changed and those sessions predated it. Taking the reading
with the furthest reset locked the machine onto the superseded window, and every correct reading
was then refused until that reset passed. Taking the soonest open reset instead names the
current window at once, and a session left behind by a real reset is refused because its window
has ended. Within one window usage only rises, so the highest value is the truth and a session
behind the others never flaps the state. Decision:
[ADR](../../adr/2026-09-20-the-open-window-that-resets-soonest-is-current.md).

A schedule change in the other direction, to a later reset, is followed only when the stored
window ends, because until then the stored window is the one resetting sooner. It corrects
itself at that reset without losing an event, since every published snapshot carries the window
it described.

Expiry is derived from the stored reset time. Claude Code appeared to drop a window once its
reset passed, but the reducer no longer depends on that: a reading whose reset has passed is
refused whoever sends it.

## Publish decision

`Decide` compares the reduced state with `state.lastPublished`. First matching row wins.

| Condition (any window) | Publish | `eventType` |
|---|---|---|
| no `lastPublished` and at least one window `observed` | yes | `state_transition` |
| a window `status` differs | yes | `state_transition` |
| a `resetsAt` differs | yes | `change` |
| abs(`used` − last published `used`) ≥ `minDeltaPercentage` | yes | `change` |
| `now − lastPublished.capturedAt ≥ heartbeatInterval` and any window `observed` | yes | `heartbeat` |
| otherwise | no | |

Both windows `unknown` never publishes, so a fresh machine emits no meaningless first event.
Published values are the actual values, never rounded. `lastPublished` is updated when the
event is spooled, not when delivered: publishing is decided locally, delivery is the spool's job.

Defaults: `minDeltaPercentage: 1.0`, `heartbeatInterval: 30m`. Example against a last published
40.0: 40.2, 40.4, 40.7 are ignored; 41.1 publishes 41.1.

## Open questions

- Monotonic usage within a window: consistent with the measurement above, where the highest of
  six readings of one window matched `/usage`. The acceptance test confirms it over a full window.
- How often the subscription's window schedule changes, and whether a change can ever move a
  reset later. The rule follows a later reset only when the stored window ends.
