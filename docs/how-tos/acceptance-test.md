# How to run the acceptance test

Practical procedure, not authoritative. Run it on a real Claude Pro or Max machine before
calling v1 complete. Record the results in the PR that flips the spec markers to `Built`.

## Prerequisites

- A machine logged into one Claude subscription, Claude Code 2.1.251 or newer.
- A Databox API key with permission to create data sources and datasets.
- `gaugewire` built from the release candidate.

## 1. Source

1. Note the current visible status line and the `statusLine` object in `~/.claude/settings.json`.
2. `gaugewire install`, then `gaugewire databox bootstrap --api-key-file <path>`.
3. Start Claude Code. Before the first model response, run `gaugewire status`: both windows
   must be `unknown`, never 0.
4. Send one message. `gaugewire status` must show both windows `observed`. Compare with
   `/usage`: same percentages, same reset times.

## 2. Existing status line

The visible status line must be functionally identical to step 1.1. Try `/compact` and a model
change: still identical.

## 3. Multiple sessions and staleness

1. Open a second session and leave it idle. Work in the first until the five-hour percentage
   rises by more than the threshold.
2. `gaugewire status` must show the higher value and never fall back to the idle session's value.
3. History in Databox must be chronological and monotonic within the window. This confirms the
   staleness guard's assumption.

## 4. Network failure

1. Disconnect the network. Cause a quota change. `gaugewire status`: pending events 1.
2. Reconnect. Within a few triggers, pending 0 and the event appears in Databox.

## 5. Databox

Current contains exactly one row for the account. History contains unique, chronological
events. Note how long a dashboard takes to reflect an ingestion and record it here:

| Date | Ingestion accepted → visible on dashboard |
|---|---|
| | |

Confirm the two assumptions from the [sink spec](../spec/gaugewire/databox-sink.md): whether
the column schema was accepted at creation, and whether a 429 was seen and what it carried.

## 6. Uninstall

`gaugewire uninstall`. The `statusLine` object in `settings.json` must equal the one noted in
step 1.1 and the rest of the file must be unchanged. Databox data remains.
