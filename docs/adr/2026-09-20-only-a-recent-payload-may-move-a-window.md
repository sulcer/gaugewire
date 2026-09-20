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
2. Take the soonest open reset. Correct until the current window resets, then the superseded
   window, which still has days to run, becomes the soonest and captures the machine again.
3. Take the most recent reading whatever it says. Eight sessions tick independently, so the state
   would flap between six values within seconds and publish on every flip.
4. Remember which session sent what. The payload's session id is deliberately never read or
   stored ([privacy](2026-09-17-claude-statusline-is-the-only-quota-source.md)), and per-session
   state would grow without bound.
5. Date the payload by its own five-hour window, and let only a dated payload say which window is
   open.

## Decision

Option 5. A five-hour window is at most five hours long, so a payload whose five-hour window has
not reset yet was taken within the last five hours. That window is also the payload's clock:
within one five-hour window its usage only rises, and a later reset is a later window, so the
pair orders payloads by age. The stored five-hour window holds the most recent pair the machine
has seen, and a payload counts as dated when it is at least that recent.

Per window:

- a reading whose reset has passed is refused, whoever sent it, because that window has ended;
- a reading of the window already stored refines it when its usage is at or above the stored
  value, whatever the payload's age, since usage only rises within a window and a session behind
  the others reports less;
- saying that a different window is open replaces what the machine reports, so it takes a dated
  payload, whether the stored window is open, ended or never seen;
- a stored window whose reset has passed retires to `expired`, whether the reading that arrived
  was absent or refused.

A payload with no five-hour window of its own proves nothing and is trusted only while the machine
has never seen one, because the subscription may have no five-hour limit at all.

The other decisions of the amended ADR still stand: the status line is the only quota source, only
the four quota fields and the version are read, the source is versioned by Claude Code's own
version, and nothing else from the payload is persisted.

## Assumptions to verify

The rule rests on three facts about Claude Code that its public documentation does not state, so
the [acceptance test](../how-tos/acceptance-test.md) checks each:

- a five-hour window is at most five hours long, which is what bounds a dated payload's age;
- both windows in one payload come from the same response, so a live five-hour window vouches for
  the seven-day window beside it;
- a subscription that has a weekly limit also has a five-hour one, failing which a machine stays
  in the trusting mode above.

## Consequences

- The measured machine corrects within one tick: the session in use is dated, the sessions that
  predate the change are not.
- A superseded window cannot come back while a dated payload is arriving, and cannot be adopted
  when the current window ends either, because naming a different window needs a dated payload.
- With nothing in use, a machine keeps the window it holds until that window ends and then reports
  `expired` with no percentage, rather than adopting a window it cannot date. It fills again from
  the first payload of a session in use.
- Two dated payloads that straddle a schedule change are ordered by the clock, so the later one
  wins and the earlier cannot take the window back: the state settles instead of alternating and
  publishing on every flip. Only payloads with the same five-hour reset *and* the same five-hour
  usage are unordered; the tie ends as soon as either session gets a response.
- When the machine adopts a window it takes whichever session's value arrives first and then
  climbs to the highest, so one correction can publish a few `change` events within seconds.
  History stays monotonic within the window.
- A machine that has never seen a five-hour window trusts any payload, which is the old behaviour
  and the only mode in which a stale session can still name a window.
- The rule reads the local clock to decide what has ended. A clock five hours fast dates no
  payload, so the machine keeps the window it holds and reports `expired` when it ends; `doctor`
  shows the windows and the age of the last observation, which is where that surfaces.
- Fixtures with fixed timestamps no longer produce an observation, so the hot-path tests move the
  fixture's two reset timestamps ahead of now, byte for byte.
- Model-specific weekly limits, which `/usage` shows separately, are not in the payload at all, so
  Gaugewire cannot report them.
