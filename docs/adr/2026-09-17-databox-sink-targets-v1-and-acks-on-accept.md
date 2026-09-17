---
tags: sink, databox, reliability
status: accepted
decision-date: 2026-09-17
---

# The Databox sink targets the documented v1 API and acknowledges on accept

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

Databox is the first sink. Its public developer documentation describes a v1 REST API with
`x-api-key` authentication, data sources, datasets with primary keys, and asynchronous
ingestion: a `POST .../data` returns an `ingestionId` and the outcome is read later from
`GET .../ingestions/{id}`. The spool must decide when a delivered event may be deleted.

## Options considered

**API version**

1. The documented v1 API.
2. Wait for or assume a newer version.

**Acknowledgement**

1. Delete the spooled event when the ingestion request is accepted (HTTP 200 with an
   `ingestionId`). `doctor` polls the latest ingestion per dataset and reports a `failed` one.
2. Keep the event spooled as `submitted` until the ingestion status endpoint reports `success`;
   dead-letter on `failed`; give up verifying after a bound.

## Decision

v1, with base path, id types and field names in one thin layer inside the sink so a future
version is a small swap. Acknowledge on accept. The records have a fixed schema, so an
asynchronous failure is a bug caught on the first run rather than an operating condition, and
the primary keys (`event_id` for History, `account_id` for Current) make any resend an upsert.

## Consequences

- At-least-once delivery holds against network and service outages; it does not hold against an
  ingestion that is accepted and then fails inside Databox. `doctor` surfaces that case.
- Batches are chronological and Current receives only the newest snapshot of a batch, guarded
  by the last delivered `capturedAt` so a requeued old event never overwrites newer state.
- Public docs do not state whether dataset creation accepts a column schema; the sink works
  with or without one and the acceptance test settles it.

## Out of scope

Verify-before-ack (see the nice-to-have ledger), other sinks.
