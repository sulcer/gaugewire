# Glossary

Status: Draft · Partial · 2026-09-17 · One meaning per term. Every spec page links a term here on first use.

| Term | Meaning |
|---|---|
| **Node** | One machine running Gaugewire. Identified by a generated UUID and a human alias such as `mac-mini-01`. |
| **Account** | The Claude subscription logged into a node. One node has exactly one account in v1. Identified by a configured UUID and an alias such as `claude-01`. Never inferred from credentials. |
| **Window** | One rate-limit window: `fiveHour` or `sevenDay`. Carries a status, a used percentage and a reset time. |
| **Window status** | `unknown` (never observed), `observed` (a valid value is held), `expired` (the stored reset time has passed and no fresh value arrived). |
| **Observation** | The four quota fields and the Claude Code version parsed from one status-line invocation. |
| **Reducer** | The pure function that folds an observation into the stored machine state, per window. |
| **Staleness guard** | The reducer rule that accepts an incoming window only if its reset time is newer, or equal with a used percentage at or above the stored one. |
| **Snapshot** | `QuotaSnapshot v1`: the normalized, versioned object that leaves the machine. Contains node, account, both windows, capture time, source and observer version. |
| **Event** | A snapshot that the dedupe step decided to publish, plus its type and delivery state. Stored as one file in the spool. |
| **Event type** | `state_transition` (a window status changed or the first observation), `change` (a reset time or a percentage moved past the threshold), `heartbeat` (nothing changed for the heartbeat interval). |
| **Dedupe** | The decision whether the reduced state differs enough from the last published state to publish again. |
| **Spool** | The `pending/` directory of event files awaiting delivery, plus `dead-letter/` for events that failed permanently. |
| **Flusher** | `gaugewire flush`: a detached, short-lived process that delivers due events to sinks and exits. |
| **Sink** | A destination adapter with `ID()` and `PublishBatch(ctx, snapshots)`. Databox is the first. |
| **Renderer** | The user's previous status-line command, which Gaugewire runs with the exact stdin bytes and whose stdout it forwards. |
| **Hot path** | Everything `gaugewire statusline` does between reading stdin and exiting. |
| **Home directory** | The one directory holding config, state, locks, spool and logs. `os.UserConfigDir()/gaugewire` or `GAUGEWIRE_HOME`. |
| **Heartbeat** | A publish forced after `heartbeatInterval` without another publish, so a dashboard can tell "unchanged" from "silent". |
| **Current** | The Databox dataset with one row per account holding the latest state. Primary key `account_id`. |
| **History** | The Databox dataset with one row per event. Primary key `event_id`. |
| **Ack** | Deleting a spooled event after the sink accepted it. |
| **Dead letter** | An event whose delivery failed permanently. Never deleted automatically; requeued with `gaugewire flush --requeue`. |
