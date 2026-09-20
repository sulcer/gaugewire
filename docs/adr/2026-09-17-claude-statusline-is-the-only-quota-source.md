---
tags: source, privacy, reducer
status: accepted
decision-date: 2026-09-17
---

# Claude Code's status-line JSON is the only quota source

Amended by: 2026-09-20-the-open-window-that-resets-soonest-is-current.md (the per-window staleness guard)

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

A fleet of machines each runs one Claude Pro or Max subscription. The owner wants one view of
the five-hour and seven-day quota across the fleet, without any risk to the accounts, without a
permanent process on the machines, and without depending on how Claude Code is hosted
(terminal multiplexer or not).

## Options considered

1. **Third-party account tools** that read stored OAuth credentials and call undocumented
   endpoints. Complete data, but they touch credentials and rely on interfaces Anthropic does not
   document or support.
2. **Terminal scraping** of the `/usage` screen through a multiplexer's pane API. Works only
   where the multiplexer runs, breaks on every rendering change, and reads whole screens.
3. **Claude Code's documented status-line JSON.** Claude Code passes `rate_limits.five_hour` and
   `rate_limits.seven_day` with `used_percentage` and `resets_at` to the configured status-line
   command, locally, on documented triggers. Confirmed live on Claude Code 2.1.274.

## Decision

Option 3, alone. Gaugewire installs itself as the status-line command, reads only the four quota
fields and the version, passes the exact stdin bytes to the user's previous renderer, and never
touches credentials, transcripts, prompts or working directories.

Because every session re-sends its own last-known values on every trigger, an idle session
would flap the machine state against an active one. Per window, an incoming value is accepted
only when its `resets_at` is newer than the stored one, or equal with a `used_percentage` at or
above the stored value. This assumes usage is monotonic within one reset window, which the
acceptance test must confirm.

## Consequences

- Only Pro and Max subscriptions expose the fields, and only after the first API response of a
  session. Absence is never zero: a window is `unknown`, `observed` or `expired`.
- Claude Code drops a window once its reset passes, so expiry is derived from stored
  `resets_at`, not from the input.
- A new trigger cancels the in-flight status-line script, which shapes the hot path: renderer
  first, no network, detached flusher.
- The staleness guard can hold a stale high value if a percentage is ever lowered mid-window.
  The window reset clears it.

## Out of scope

Account switching, rotation, or any automation that acts on the accounts.
