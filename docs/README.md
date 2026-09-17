# Gaugewire documentation

Gaugewire observes Claude Code subscription quota on each machine of a fleet and publishes it
to a pluggable observability destination. Databox is the first destination.

## Trust hierarchy

Read in this order and trust in this order. When two documents disagree, the higher one wins.

1. `AGENTS.md` at the repo root (created by the scaffold plan). The rulebook entry point.
2. `.claude/rules/` (created by the scaffold plan). Detailed rules, most of them path-scoped.
3. [`adr/`](adr/README.md). Accepted decisions, dated, never deleted, superseded or amended by
   newer ADRs.
4. [`spec/`](spec/README.md). Living specs that state the current design as fact.
5. [`how-tos/`](how-tos/). Practical procedures. Useful, not authoritative.

[`research/`](research/) is frozen input. It is never cited from a spec, an ADR or code except
through the one-line provenance footer at the bottom of a spec page.

## Map

| Folder or file | Holds |
|---|---|
| [`adr/`](adr/README.md) | One file per decision, `YYYY-MM-DD-kebab-title.md`, indexed in its README |
| [`spec/`](spec/README.md) | One folder per topic. `gaugewire/` is the product, `repo-scaffold/` is how the repo is built |
| [`plans/`](plans/) | Implementation plans. Committed on the feature branch, deleted when the feature lands |
| [`how-tos/`](how-tos/) | Step-by-step procedures: acceptance test, releasing, local development |
| [`research/`](research/) | Frozen inputs: the original product spec and the design conversation |
| [`why-these-rules.md`](why-these-rules.md) | The reasoning and incidents behind every rule, kept out of always-on context |
| [`nice-to-have.md`](nice-to-have.md) | Deferred work: what, why deferred, trigger to revisit, reference |

## Start here

- New to the product: [`spec/gaugewire/README.md`](spec/gaugewire/README.md), then the
  [glossary](spec/gaugewire/glossary.md), then [architecture](spec/gaugewire/architecture.md).
- New to the repo: [`spec/repo-scaffold/README.md`](spec/repo-scaffold/README.md).
- Why a rule exists: [`why-these-rules.md`](why-these-rules.md).
