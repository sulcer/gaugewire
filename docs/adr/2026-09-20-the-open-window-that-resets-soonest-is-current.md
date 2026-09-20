---
tags: domain, reducer, staleness
status: accepted
decision-date: 2026-09-20
---

# The open window that resets soonest is the current one

Amends: 2026-09-17-claude-statusline-is-the-only-quota-source.md (decision: the per-window staleness guard)

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

The amended ADR keeps a reading only when its reset is newer than the stored one, or equal with
usage at or above it. That assumed a window's reset time only ever moves forward.

Measured on a real machine with eight open Claude Code sessions, each running the status line
every five seconds, 106 payloads in 25 seconds:

| Sessions | Seven-day usage reported | Reset carried |
|---|---|---|
| 6 | 78, 76, 66, 64, 44, 27 per cent | the same time, that night |
| 2 | 7 and 1 per cent | four days later |

`/usage` reported 78 per cent, matching the highest reading of the first group. Each session
sends the rate limits it last received, so the six readings were one window seen at six different
moments. The two others carried a reset that no longer applied: the subscription's weekly schedule
had changed and those sessions predated the change. Only the session in use reported the
five-hour window at all; idle ones omitted it.

Under the amended rule the machine took the furthest reset, locked onto the superseded window at
7 per cent, and refused every correct reading for the four days until that reset passed. The
machine published, and would have kept publishing, a number an order of magnitude below the truth.

## Options considered

1. Keep the newest reset. What was measured: a superseded window wins and holds for days.
2. Take the most recent reading whatever it says. Eight sessions tick independently, so the state
   would flap between six values within seconds and publish on every flip.
3. Take the reading with the highest usage across windows. Correct here by luck. Right after a
   real reset it prefers the ended window, which still reports high usage, over the new one.
4. Ignore a reading whose reset has passed; among the rest prefer the soonest reset; within one
   reset keep the highest usage.

## Decision

Option 4. A window is identified by its reset time. A reading whose reset has passed describes a
window that has ended and is refused, whoever sends it. Among windows still open, the one
resetting soonest is the current one, because a later reset can only come from a session whose
values predate a schedule change. Within one window usage only rises, so the highest reading wins
and a session behind the others cannot flap the state. A stored window whose reset has passed
retires to `expired` whether the reading that arrived was absent or refused.

## Consequences

- A superseded schedule is dropped as soon as one session reports the current window.
- A real reset is followed immediately: sessions left behind carry a window that has ended.
- A schedule change to a later reset is followed only when the stored window ends, up to one
  window late. It costs no event, because every published snapshot carries the window it described.
- A payload whose windows have all ended observes nothing, so a machine that has been idle since
  its last reset reports `unknown` rather than a stale percentage. Fixtures with fixed timestamps
  no longer produce an observation, so the hot-path tests build payloads whose windows are open.
- Model-specific weekly limits, which `/usage` shows separately, are not in the status-line
  payload at all, so Gaugewire cannot report them.
