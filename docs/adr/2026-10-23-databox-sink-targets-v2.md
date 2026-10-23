---
tags: sink, databox, config
status: accepted
decision-date: 2026-10-23
---

# The Databox sink targets the documented v2 API

Amends: 2026-09-17-databox-sink-targets-v1-and-acks-on-accept.md (decision: the API version)
Amends: 2026-09-19-doctor-judges-ingestions-by-rejected-records.md (decision: the field doctor reads)

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

Databox has published v2 of its REST API, with a migration guide
(https://developers.databox.com/docs/api/migration-guide, fetched 2026-10-23). v1 "continues to
run at its existing base URL", no shutdown date is published, and new functionality is added
only to v2. Every endpoint the sink calls changes shape in v2:

- success payloads move under `data`, and lists become paginated `data.items`;
- there is no account list: requests act on the key's organization, or on one of its accounts
  through an `x-account-id` header;
- data sources and datasets are named by `name` rather than `title`, and dataset ids are
  integers rather than UUID strings;
- dataset creation requires a column `schema`;
- the ingestion status replaces `metrics` with `summary` and an `errors` array.

The error-codes page also states that an error code can arrive in a 2xx body.

## Options considered

1. Stay on v1 until a shutdown date is announced.
2. Move to v2 now, keeping the one thin client layer the v1 ADR asked for.

For the dataset ids already saved in `config.json`:

1. Keep them as strings and parse them at use.
2. Make them integers and fail loudly on a v1 file. `config.Load` then fails, so the status line
   stops observing and bootstrap, which loads the same file first, cannot repair it.
3. Make them integers and read a leftover string as unset.

## Decision

Move to v2 (option 2), with dataset ids as integers that read a leftover v1 string as unset
(option 3). A v1 `config.json` still loads, so observation and the spool keep working; the sink
refuses to build with both dataset ids unset ("run gaugewire databox bootstrap"), and the next
bootstrap writes the v2 ids.

Bootstrap reads `GET /v2/organization` instead of choosing among listed accounts.
`--account-id` is sent as `x-account-id` on every request and saved as `accountId`; like
`--base-url` it is recorded on every run, so a run without it acts on the organization itself.
Datasets are created with an explicit schema typed by the rule the spec already stated:
`*_at` columns are `datetime`, `*_used_percentage` columns are `number`, and every other
column is a `string`. When bootstrap changes a dataset id it resets the sink's ingestion
record, whose ids belong to the datasets replaced.

An error inside a 2xx body is classed by its code: `rate_limited`, `internal_error`,
`service_unavailable` and `processing_failed` are retried, every other code is permanent, and
an error with no code is a malformed response and retried. 404 is `not_found` and 413 is `request_too_large`, both permanent.

`doctor` keeps judging an ingestion by its rejected count, now
`summary.ingestion.rejectedRecordCount`, and shows the first entry of `errors` beside it.

Still standing from the amended ADRs: a spooled event is deleted once the ingestion request is
accepted, Current is guarded by the last delivered `capturedAt`, and `status` is shown but
never judged.

## Consequences

- Every machine runs `gaugewire databox bootstrap` once after upgrading. Until then flush fails
  and events stay spooled; nothing is lost.
- Whether datasets created through v1 appear in v2 lists is not documented. If they do not,
  bootstrap creates two new datasets and the dashboards start from empty. The acceptance test
  settles it.
- The 100-record cap per ingestion stays: v2 documents 500 rows per request and no smaller
  per-ingestion limit.
- The optional `Idempotency-Key` header is not sent: both datasets upsert by primary key, so a
  resent ingestion is already safe.
