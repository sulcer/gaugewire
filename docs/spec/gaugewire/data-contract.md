# Data contract

Status: Draft · Partial · 2026-09-19 · Every persisted or transmitted shape: the snapshot, window status, event types, config, state and spooled events.

## At a glance

Sinks never see Anthropic's raw schema. They receive `QuotaSnapshot v1`, a versioned object
owned by this project. Everything on disk is JSON written atomically. Only the four quota fields
and the Claude Code version are ever read from the status-line input; nothing else from it is
persisted. The snapshot, state, spooled-event and config shapes are built; the identity wiring
runs through `config.json`.

## Source input

Read from stdin, only these paths:

```
version
rate_limits.five_hour.used_percentage    rate_limits.five_hour.resets_at
rate_limits.seven_day.used_percentage    rate_limits.seven_day.resets_at
```

`rate_limits` appears only for Pro and Max subscriptions and only after the first API response
of a session; each window may be independently absent; Claude Code drops a window once its
`resets_at` passes. Never persisted: `session_id`, `prompt_id`, `cwd`, `transcript_path`,
`workspace`, `model`, cost, context or cache fields. Claude Code `2.1.251` or newer is required;
older or missing versions skip observation and `doctor` reports it.

## QuotaSnapshot v1

```json
{
  "schemaVersion": 1,
  "eventId": "uuid-v4",
  "node":    { "id": "node-uuid", "alias": "mac-mini-01", "platform": "darwin" },
  "account": { "id": "account-uuid", "alias": "claude-01" },
  "capturedAt": "2026-09-17T15:30:00Z",
  "windows": {
    "fiveHour": { "status": "observed", "usedPercentage": 24,   "resetsAt": "2026-09-17T16:20:00Z" },
    "sevenDay": { "status": "observed", "usedPercentage": 53.5, "resetsAt": "2026-09-18T07:00:00Z" }
  },
  "source": { "type": "claude-code-statusline", "claudeCodeVersion": "2.1.274" },
  "observerVersion": "1.0.0"
}
```

| Field | Type | Rules |
|---|---|---|
| `schemaVersion` | integer | `1`. A breaking change bumps it and gets an ADR. |
| `eventId` | UUID v4 | Generated when the event is spooled. History primary key. |
| `node.platform` | `darwin`, `linux`, `windows` | `runtime.GOOS`. |
| `capturedAt` | RFC 3339 UTC, at most millisecond precision (trailing zeros trimmed) | Wall clock at the hot path. |
| `windows.*.status` | `unknown`, `observed`, `expired` | See [reducer](reducer-and-dedupe.md). |
| `windows.*.usedPercentage` | number or `null` | `0 ≤ x ≤ 100`, never rounded, `null` unless `observed`. `0` is a real value. |
| `windows.*.resetsAt` | RFC 3339 UTC or `null` | Kept through `expired` for diagnostics. |
| `observerVersion` | string | Injected at build time. |

`NewSnapshot` normalises `capturedAt` to UTC and truncates it to milliseconds.

## Event types

| `eventType` | When |
|---|---|
| `state_transition` | first observation, or a window status changed |
| `change` | a `resetsAt` changed, or a percentage moved at least `minDeltaPercentage` from the last published value |
| `heartbeat` | `heartbeatInterval` elapsed since the last publish while the status line is active |

## config.json

```json
{
  "schemaVersion": 1,
  "node":    { "id": "uuid-v4", "alias": "mac-mini-01" },
  "account": { "id": "uuid-v4", "alias": "claude-01" },
  "renderer": { "command": "bash ~/.claude/statusline-command.sh" },
  "install": {
    "settingsPath": "/Users/me/.claude/settings.json",
    "installedCommand": "/usr/local/bin/gaugewire statusline",
    "originalStatusLine": { "type": "command", "command": "bash ~/.claude/statusline-command.sh", "refreshInterval": 5 }
  },
  "publishing": { "minDeltaPercentage": 1.0, "heartbeatInterval": "30m" },
  "sinks": [
    {
      "id": "databox-main", "type": "databox", "enabled": true,
      "baseUrl": "https://api.databox.com",
      "accountId": 123456, "dataSourceId": 4754489,
      "currentDatasetId": "uuid", "historyDatasetId": "uuid",
      "credentials": { "apiKeyEnv": "DATABOX_API_KEY", "apiKeyFile": "" }
    }
  ]
}
```

- An empty or absent `renderer.command` means no renderer: Gaugewire prints nothing.
- `install.originalStatusLine` is kept as raw JSON so uninstall restores the same object; it is
  the original object JSON-equal to what install found, not its original source bytes, and it is
  omitted entirely (never `null`) when install found no `statusLine` to save. `install.settingsPath`
  is always an absolute path. `install` is absent until `install` runs, and `uninstall` clears it;
  `databox bootstrap` never touches it.
- Durations are Go duration strings. Unknown sink `type` fails validation.
- Credentials: `apiKeyFile` if set, else the environment variable named by `apiKeyEnv`
  (default `DATABOX_API_KEY`). The key never appears in config, state, events or logs.
- `sinks[].baseUrl` is set by `gaugewire databox bootstrap`, which defaults it to the public API
  host and overwrites it on every run; it exists so a test can point the sink at `httptest`.
- Loading a file that decodes but fails validation returns the decoded value together with an
  `ErrInvalid` error, so the hot path can keep the renderer running; every other command treats
  it as fatal.

## state.json

```json
{
  "schemaVersion": 1,
  "windows": { "fiveHour": { "...": "..." }, "sevenDay": { "...": "..." } },
  "lastObservedAt": "2026-09-17T15:30:00Z",
  "claudeCodeVersion": "2.1.274",
  "lastPublished": { "eventId": "...", "capturedAt": "...", "windows": { "...": "..." } },
  "lastFlush": { "at": "...", "ok": true, "error": "" },
  "lastIngestion": {
    "databox-main": { "current": "ingestionId", "history": "ingestionId", "currentCapturedAt": "...", "at": "..." }
  }
}
```

The hot path writes `windows`, `lastObservedAt`, `claudeCodeVersion` and `lastPublished` under
`state.lock`. The flusher writes `lastFlush` under `state.lock`, held only around the
read-modify-write, never around network calls. `lastIngestion` is written by the Databox sink
itself, keyed by sink id, under the same lock, once the chunk's History request — and its
Current request, when the guard sends one — has been accepted.

## Spooled event

`pending/<capturedAtUnixMillis>-<eventId>.json`:

```json
{
  "eventType": "change",
  "snapshot": { "...QuotaSnapshot v1...": "" },
  "delivery": {
    "databox-main": { "attempts": 0, "nextAttemptAt": "...", "lastError": "", "lastErrorCode": "" }
  }
}
```

`delivery` is keyed by the sink ids enabled at capture time. A dead-lettered event moves to
`dead-letter/` with the same name plus `deadLetteredAt` and `reason`.

## Open questions

None.
