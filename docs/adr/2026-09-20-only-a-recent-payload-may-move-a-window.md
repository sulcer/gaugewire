---
tags: domain, reducer, staleness
status: accepted
decision-date: 2026-09-20
---

# Only a payload from the last five hours may move a window

Amends: 2026-09-17-claude-statusline-is-the-only-quota-source.md (decision: the per-window staleness guard)

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

The amended ADR keeps a reading only when its reset is newer than the stored one, or equal with
usage at or above it. That assumed a window's reset only ever moves forward.

Measured on a real machine with eight open Claude Code sessions, each running the status line
every five seconds, 106 payloads in 25 seconds:

| Sessions | Seven-day usage reported | Reset carried | Five-hour window |
|---|---|---|---|
| 6 | 78, 76, 66, 64, 44, 27 per cent | that night | only the session in use carried one |
| 2 | 7 and 1 per cent | four days later | none |

`/usage` reported 78 per cent, matching the highest reading of the first group. Each session sends
the rate limits it last received, so those six were one window seen at six moments. The two others
carried a reset that no longer applied: the subscription's weekly schedule had changed and those
sessions predated it.

Under the amended rule the machine took the furthest reset, locked onto the superseded window at
7 per cent, and refused every correct reading for the four days until that reset passed. It
published, and would have kept publishing, a number an order of magnitude below the truth.

Reset order alone cannot fix this. Taking the soonest open reset instead is correct while the
current window runs, and wrong the moment it resets: the superseded window still has days left,
so it becomes the soonest and captures the machine again. Something other than the reset has to
say which payload is current.

## Options considered

1. Keep the newest reset. What was measured: a superseded window wins for days.
2. Take the soonest open reset. Correct until the current window resets, then the same lock-in in
   the other direction.
3. Take the most recent reading whatever it says. Eight sessions tick independently, so the state
   would flap between six values within seconds and publish on every flip.
4. Remember which session sent what. The payload's session id is deliberately never read or
   stored ([privacy](2026-09-17-claude-statusline-is-the-only-quota-source.md)), and per-session
   state would grow without bound.
5. Use the five-hour window as proof of the payload's age.

## Decision

Option 5. A five-hour window is five hours long, so a payload whose five-hour window has not reset
yet was taken within the last five hours. Windows partition time, so under one schedule every
payload with time left on the clock names the same reset; two open resets mean two schedules, and
a payload from the last five hours is the one that knows which schedule holds.

Per window:

- a reading whose reset has passed is refused, whoever sent it, because that window has ended;
- a reading of the window already stored is kept when its usage is at or above the stored value,
  whatever the payload's age, since usage only rises within a window;
- a reading of a different open window is taken only from a payload of the last five hours;
- a stored window whose reset has passed retires to `expired`, whether the reading that arrived
  was absent or refused.

The five-hour window is judged by the same rule, where the proof is the reading itself.

The other decisions of the amended ADR still stand: the status line is the only quota source, only
the four quota fields and the version are read, the source is versioned by Claude Code's own
version, and nothing else from the payload is persisted.

## Consequences

- A superseded schedule is dropped as soon as the session in use reports the current window, and
  cannot come back: no older payload may move a window.
- A real reset is followed immediately. Sessions left behind carry a window that has ended, and
  their readings are refused rather than resurrecting it.
- A machine whose every session has been idle for more than five hours keeps the window it holds
  until that window ends, then reports `expired` with no percentage. It cannot follow a schedule
  change while nothing is in use, which is also when its numbers matter least.
- If the schedule changes and two sessions both had a response within five hours on either side
  of the change, each may move the window back, so the state can alternate until the older of the
  two passes five hours. Bounded, rare, and visible in history as alternating resets.
- When the machine adopts the current window it takes whichever session's value arrives first and
  then climbs to the highest, so one correction can publish a few `change` events within seconds.
  History stays monotonic within the window.
- A payload whose windows have all ended observes nothing. A machine that has never observed a
  window reports `unknown`; one that has reports `expired`, keeping the reset for diagnostics.
- The rule reads the local clock to decide what has ended. A clock more than a window fast refuses
  every reading of that window; `doctor` reports the windows and the age of the last observation,
  which is where that shows up.
- Fixtures with fixed timestamps no longer produce an observation, so the hot-path tests move the
  fixture's two reset timestamps ahead of now, byte for byte.
- Model-specific weekly limits, which `/usage` shows separately, are not in the payload at all, so
  Gaugewire cannot report them.
