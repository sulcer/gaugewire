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
within one five-hour window its usage only rises, and a reset later than the one the machine
holds is a later window once that one has ended, so the pair orders payloads by age. While the
window the machine holds is still running, a later reset is that window re-anchored under the
payload, not a payload taken later, and it dates nothing. The stored five-hour window holds the most recent pair the machine
has seen, and a payload is level with the clock when it matches that pair and past it when it is
further along.

Per window:

- a reading whose reset has passed is refused, whoever sent it, because that window has ended;
- a reading of the window already stored refines it when its usage is at or above the stored
  value, whatever the payload's age, since usage only rises within a window and a session behind
  the others reports less;
- naming a window the machine does not hold, because it is `unknown` or has ended, takes a
  payload at least level with the clock;
- taking a window that is still open and calling it something else takes a payload past the
  clock. Two payloads level with each other are unordered, and a rule that let either replace the
  other would let two sessions trade the window on every tick, publishing each time;
- a stored window whose reset has passed retires to `expired`, whether the reading that arrived
  was absent or refused.

Asking for a payload past the clock on every move would be one rule instead of two, but then a
seven-day reset would report `expired` until some session's five-hour usage moved, so the strict
test is kept for the case that can flap and no wider.

A payload with no five-hour window of its own proves nothing and is trusted only while the machine
has never seen one, because the subscription may have no five-hour limit at all. Even then it
never counts as past the clock, so it can fill a window the machine does not hold but not take
one that is open.

The other decisions of the amended ADR still stand: the status line is the only quota source, only
the four quota fields and the version are read, the source is versioned by Claude Code's own
version, and nothing else from the payload is persisted.

## Assumptions to verify

The rule rests on five facts about Claude Code that its public documentation does not state, so
the [acceptance test](../how-tos/acceptance-test.md) checks each:

- a five-hour window is at most five hours long, which is what bounds a dated payload's age;
- within one five-hour window the reported usage only rises, which is what orders two payloads
  carrying the same five-hour reset;
- a five-hour window is not re-anchored while it is running, so two payloads inside one window
  carry the same reset. The seven-day schedule was re-anchored, which is what this ADR exists for,
  so the same is assumed possible here and refused rather than trusted;
- both windows in one payload come from the same response, so a live five-hour window vouches for
  the seven-day window beside it;
- a subscription that has a weekly limit also has a five-hour one, failing which a machine stays
  in the trusting mode above.

## Consequences

- A machine with no window stored takes the first dated payload, so the measured fleet corrects on
  the first tick of a session in use, and the stale sessions cannot take the window back.
- A machine already holding the superseded window, which is what the measurement left behind,
  holds it until a payload past its own clock arrives. The clock there is the in-use session's own
  five-hour pair, so the correction lands on the first tick whose five-hour usage has risen, or
  when that five-hour window resets: within five hours, and within minutes on a machine being
  worked on.
- A superseded window cannot take a window the machine holds open, because that takes a payload
  past the clock. Once the window the machine holds has ended, a payload merely level with the
  clock may name the next one, so a stale session level with the clock can put a superseded window
  in at that moment. The first payload past the clock corrects it, where round one held the wrong
  window for days.
- With nothing in use, a machine keeps the window it holds until that window ends and then reports
  `expired` with no percentage, rather than adopting a window it cannot date. It fills again from
  the first payload of a session in use.
- Two sessions that straddle a schedule change are ordered by the clock, so the later one wins and
  the earlier cannot take the window back: the state settles instead of alternating and publishing
  on every flip. Two payloads level with each other are unordered, and neither takes the window
  from the other, so a machine can hold the earlier of the two until a session gets a response.
- A window the machine holds open is only replaced by a payload past the clock, so a schedule
  change seen while the five-hour reading has not moved is followed on the next tick that moves
  it, not immediately.
- When the machine adopts a window it takes whichever session's value arrives first and then
  climbs to the highest, so one correction can publish a few `change` events within seconds.
  History stays monotonic within the window.
- A machine that has never seen a five-hour window trusts any payload to fill a window it does not
  hold. That is rejected option 3 narrowed to the one case where nothing better exists, and the
  only mode in which a stale session can still name a window.
- The clock can be frozen by its own content in three cases, each bounded by five hours: a
  five-hour window re-anchored to an earlier reset dates no later payload, a genuine re-anchor to a
  later reset is read as a re-anchor and ignored until the window the machine holds ends, and a
  five-hour window that has ended leaves an idle machine with nothing to date by. In both the machine keeps the windows it
  holds and retires them as their resets pass.
- The rule reads the local clock to decide what has ended. A clock five hours fast dates no
  payload, so the machine keeps the window it holds and reports `expired` when it ends. The
  five-hour window's own status is where that surfaces: `expired` or `unknown` beside a fresh
  `lastObservedAt`, which `status` and `doctor` both show. `lastObservedAt` alone would not, since
  it records an undated payload too.
- A machine with no state file fills each window from the first payload that carries it, five-hour
  window or not, because a machine that has never seen one has no clock to fall behind.
- Fixtures with fixed timestamps no longer produce an observation, so the hot-path tests move the
  fixture's two reset timestamps ahead of now, byte for byte.
- Model-specific weekly limits, which `/usage` shows separately, are not in the payload at all, so
  Gaugewire cannot report them.
