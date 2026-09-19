---
tags: sink, databox, doctor
status: accepted
decision-date: 2026-09-19
---

# Doctor judges an ingestion by its rejected records

Amends: 2026-09-17-databox-sink-targets-v1-and-acks-on-accept.md (decision: doctor reports a failed ingestion)

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

The amended ADR acknowledges a delivery once the ingestion request is accepted and has `doctor`
poll the latest ingestion per dataset and report a `failed` one. The documented ingestion status
response, `GET /v1/datasets/{id}/ingestions/{ingestionId}` (https://developers.databox.com,
fetched 2026-09-19), carries no failure state: its `status` is the response envelope's, `success`
whenever the request itself succeeded, and it has no error list. What it does carry is
`ingestionMetrics`: `appendedRecordsCount`, `receivedRecordsCount`, `rejectedRecordsCount` and
`overwrittenRecordsCount`. A check that waits for `failed` would never fire.

## Options considered

1. Keep waiting for a `failed` status. It never appears in the documented shape.
2. Judge an ingestion by `rejectedRecordsCount`: above zero is a failure.
3. Compare `receivedRecordsCount` with `appendedRecordsCount + overwrittenRecordsCount`. The
   documentation does not state that relation, so the check would rest on an assumption.

## Decision

Option 2. `doctor`'s last-ingestion row fails when either dataset's latest ingestion reports a
`rejectedRecordsCount` above zero, and names the count; `status` is shown but never judged.

The other decisions of the amended ADR still stand: the sink targets the documented v1 API
behind one thin layer, a spooled event is deleted once the ingestion request is accepted, and
Current is guarded by the last delivered `capturedAt`.

## Consequences

- A record the API accepted and then rejected shows up as, for example,
  `current success (2 rejected)`, with the row failed and `doctor` exiting 1.
- A failure the metrics do not count stays invisible to `doctor`; the acceptance test is where
  such a case would surface.
- Verify-before-ack stays out of scope, as in the amended ADR.
