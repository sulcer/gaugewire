# Architecture Decision Records

Every decision someone would otherwise re-litigate, newest first. Format, supersession and
amendment rules live in `.claude/rules/always-on/adr-and-spec-discipline.md` and are summarised
in the
[documentation system spec](../spec/repo-scaffold/README.md#documentation-system).

Read the relations column before trusting an ADR. `accepted` does not mean wholly current: an
amended ADR keeps that status while some of its decisions have been reversed. Follow its
`Amended by:` link to find which.

| Date | ADR | Decision | Status | Relations |
|---|---|---|---|---|
| 2026-09-20 | [only-a-recent-payload-may-move-a-window](2026-09-20-only-a-recent-payload-may-move-a-window.md) | A reading whose reset has passed is refused; a payload dated by its own five-hour window names a window the machine does not hold, and only one past that clock takes a window still open; within one window the highest usage wins, whatever the payload's age; a five-hour reset later than the one held counts only once that one has ended | accepted | Amends claude-statusline-is-the-only-quota-source (the staleness guard) |
| 2026-09-19 | [one-account-id-per-subscription](2026-09-19-one-account-id-per-subscription.md) | Every machine on one Claude subscription shares one account id, set with `install --account-id`; Current holds one row per subscription and the last writer wins across machines | accepted | |
| 2026-09-19 | [doctor-judges-ingestions-by-rejected-records](2026-09-19-doctor-judges-ingestions-by-rejected-records.md) | `doctor` reports an ingestion as failed when its `rejectedRecordsCount` is above zero, since the documented ingestion status carries no failure state | accepted | Amends databox-sink-targets-v1-and-acks-on-accept (doctor's ingestion check) |
| 2026-09-17 | [documentation-system](2026-09-17-documentation-system.md) | Dated ADRs, living specs with two markers, ephemeral plans, Mermaid for flows, frozen research, third-party behaviour cited from public documentation only | accepted | |
| 2026-09-17 | [gates-as-hooks](2026-09-17-gates-as-hooks.md) | Every mechanical check runs as a Claude Code hook, a plain git hook and a CI job using the same command; CI tests Ubuntu and Windows per PR, macOS on main and tags | accepted | |
| 2026-09-17 | [go-toolchain-and-pinned-tools](2026-09-17-go-toolchain-and-pinned-tools.md) | Go 1.27 with a toolchain directive, dev tools pinned in `tools/go.mod`, standard library first, `encoding/json` v1 API | accepted | |
| 2026-09-17 | [json-config-in-one-home-directory](2026-09-17-json-config-in-one-home-directory.md) | `config.json` and all state under one platform config directory, overridable by `GAUGEWIRE_HOME` | accepted | |
| 2026-09-17 | [databox-sink-targets-v1-and-acks-on-accept](2026-09-17-databox-sink-targets-v1-and-acks-on-accept.md) | The Databox sink uses the documented v1 REST API and deletes a spooled event once the ingestion request is accepted | accepted | Amended by doctor-judges-ingestions-by-rejected-records (doctor's ingestion check) |
| 2026-09-17 | [claude-statusline-is-the-only-quota-source](2026-09-17-claude-statusline-is-the-only-quota-source.md) | Quota comes only from Claude Code's documented status-line JSON; a per-window staleness guard reconciles concurrent sessions | accepted | Amended by only-a-recent-payload-may-move-a-window (the staleness guard) |
