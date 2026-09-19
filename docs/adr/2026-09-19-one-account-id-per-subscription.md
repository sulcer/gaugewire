---
tags: identity, install, fleet, databox
status: accepted
decision-date: 2026-09-19
---

# One account id per subscription, shared across machines

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

The Current dataset is keyed by `account_id` and promises one row per Claude subscription. The
fleet model makes identity configured, never inferred. Until now `install` generated a new
account id on every machine that had none, so a subscription used on several machines got one
Current row per machine unless someone copied the id into each `config.json` by hand.

## Options considered

1. Keep one id per machine and let dashboards aggregate. Current then holds one row per machine,
   which contradicts its own definition.
2. `gaugewire install --account-id <uuid>`: the first machine's install generates the id and
   prints it on its account line; every other machine on the subscription passes that id.
3. Derive the id from something Claude Code reports. That is inference, which the fleet model
   rules out, and it would rest on a field the status-line documentation does not describe as a
   stable subscription identifier.

## Decision

Option 2. `--account-id` must parse as a UUID and is stored in canonical lowercase form, so two
machines never differ by letter case. It overrides the saved id, so an installed machine joins a
subscription with `gaugewire install --force --account-id <uuid>`. Without the flag, install keeps
the saved id or generates one, as before.

## Consequences

- Current holds one row per subscription when every machine on it shares the id.
- Across machines the last writer wins on Current. Each machine's `currentCapturedAt` guard knows
  only its own deliveries, so a machine that delivers late can overwrite a newer row written by
  another machine until the next update from any of them. History keeps every event with its
  `node_id`, so nothing is lost.
- A mistyped id that is still a valid UUID creates a second Current row. It shows up as an extra
  account in the dataset and is fixed by reinstalling with the right id.
