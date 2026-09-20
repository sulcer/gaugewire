# Reducer and dedupe

Status: Draft · Built · 2026-09-20 · How one observation becomes machine state, and when state becomes an event.

## At a glance

Each window is reduced independently through a three-state machine. A missing window is never
zero. Every open Claude Code session runs the status line with the rate limits it last received,
so one machine sees many readings of one window, and after a schedule change readings of a
window that no longer applies. A window is identified by its reset time: a reading of a window
that has ended says nothing, any payload may refine the window already stored because usage
only rises within it, and saying that a different window is open takes a payload dated by its
own five-hour window. Publishing is decided against the last published snapshot, not the
previous input, so small drifts accumulate until they cross the threshold.

## Diagram

```mermaid
stateDiagram-v2
    [*] --> unknown
    unknown --> observed: open reading, payload at least level with the clock
    observed --> observed: same resetsAt and used ≥ stored, any payload
    observed --> observed: different resetsAt, stored still open, payload past the clock
    observed --> observed: reading refused or absent, stored resetsAt > now (keep)
    observed --> expired: reading refused or absent, stored resetsAt ≤ now
    expired --> observed: open reading, payload at least level with the clock
    unknown --> unknown: absent, every reading already ended, or no dated payload
```

## Validation

An incoming window is valid when `0 ≤ used_percentage ≤ 100` and `resets_at` is a positive
integer of epoch seconds. An invalid window is treated as absent for that invocation; the parser
reports the failing window path (`rate_limits.five_hour` or `rate_limits.seven_day`) to its
caller, and the hot path (not yet built) logs it once. The
`version` must parse and be at least `2.1.251`; otherwise the whole observation is skipped and
rendering continues.

## Reducer rules, per window

| Incoming reading | Stored | Payload | Result |
|---|---|---|---|
| `resetsAt ≤ now` | any | any | ignore: that window has ended |
| open, same `resetsAt` | `observed` | any, dated or not | accept when `used ≥ stored`, else ignore (a session behind the others) |
| open, different `resetsAt` | `observed` and `resetsAt > now` | past the clock | accept: `observed`, copy value and reset |
| open, different `resetsAt` | `observed` and `resetsAt > now` | level with the clock, or undated | ignore: a window still open is not taken on a tie |
| open, different `resetsAt` | `unknown`, or `resetsAt ≤ now` | at least level with the clock | accept: `observed`, copy value and reset |
| open, different `resetsAt` | `unknown`, or `resetsAt ≤ now` | undated | ignore: only a dated payload names a window |
| absent, or ignored | `unknown` | any | stay `unknown` |
| absent, or ignored | stored `resetsAt > now` | any | keep stored |
| absent, or ignored | stored `resetsAt ≤ now` | any | `expired`, `usedPercentage = null`, keep `resetsAt` |

**Why the guard exists.** Measured on a machine with eight open sessions, each ticking every
five seconds: seven-day readings of 27, 44, 64, 66, 76 and 78 per cent all carried the same
reset, one per session's cache, and the highest matched what Claude Code's `/usage` reported.
Two further sessions carried a different reset, four days later, at 7 and 1 per cent: the
schedule of the weekly window had changed and those sessions predated it. Taking the reading
with the furthest reset locked the machine onto the superseded window, and every correct reading
was then refused until that reset passed. Reset order alone cannot settle it: taking the
soonest open reset is right until the current window ends, and then the superseded window,
which still has days left, becomes the soonest and captures the machine again.

What settles it is the five-hour window, which dates the payload carrying it. It is at most
five hours long, so a payload whose five-hour window has not reset yet was taken within the
last five hours, and the window doubles as a clock: within it usage only rises, and a later
reset is a later window once the one the machine holds has ended, so the pair orders payloads by
age. A later reset while the machine's own five-hour window is still running is that window
re-anchored, which is what happened to the weekly schedule, so it dates nothing. The stored
five-hour window holds the newest pair the machine has seen. A payload at least level with it may name a window the
machine does not hold, because that window is `unknown` or has ended; taking a window that is
still open and calling it something else takes a payload past the clock, so two sessions level
with each other cannot trade the window back and forth on every tick. Every other payload may
still raise the usage of the window already stored. In the measurement only the session in use
carried a five-hour window at all. A machine that has never seen one trusts any payload to fill
a window it does not hold, because the subscription may have no five-hour limit. Decision:
[ADR](../../adr/2026-09-20-only-a-recent-payload-may-move-a-window.md).

`lastObservedAt` records that a payload arrived, including one whose readings were all
refused, so a fresh observation time can sit above a window that is `expired`.

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
- How often the subscription's window schedule changes. A change is followed on the first tick
  of a session in use whose five-hour reading has moved past the one the machine holds; while
  every session has been idle for more than five hours, the machine keeps the window it holds
  until that window ends and then reports it as expired.
- The four facts the dating rests on, listed in the
  [ADR](../../adr/2026-09-20-only-a-recent-payload-may-move-a-window.md) and checked by the
  [acceptance test](../../how-tos/acceptance-test.md): the five-hour window's length, its usage
  only rising within it, both windows coming from one response, and whether a weekly limit always
  comes with a five-hour one.
