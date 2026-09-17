---
tags: docs, adr, spec, agents
status: accepted
decision-date: 2026-09-17
---

# Documentation system: dated ADRs, living specs, ephemeral plans, Mermaid flows

## Members

Repo owner (@sulcer), Claude Code.

## Status

accepted

## Context and Problem Statement

The repo is built by humans and AI agents. Both need one answer to "which document wins?", a
place for decisions that will otherwise be re-litigated, a current-state description of the
product that is not frozen at ship, and somewhere for implementation plans that does not turn
into a graveyard.

## Options considered

- Numbered versus dated ADR filenames; a template file versus a rule that is the template.
- Specs frozen at ship versus edited in place with ADRs recording behaviour changes.
- Plans kept forever, kept out of the repo, or committed and deleted when the work lands.
- Hand-authored SVG diagrams versus Mermaid fenced blocks.
- Citing undocumented third-party behaviour versus public documentation only.

## Decision

1. Trust hierarchy, highest first: `AGENTS.md`, `.claude/rules/`, `docs/adr/`, `docs/spec/`,
   `docs/how-tos/`. `docs/research/` is frozen and cited only through a spec's provenance
   footer.
2. ADRs are `docs/adr/YYYY-MM-DD-kebab-title.md` with `tags`, `status`, `decision-date`
   frontmatter and fixed sections. They are never deleted; a newer ADR supersedes or amends,
   and both files carry the relation line right after the title. The bar is a decision someone
   would otherwise re-litigate.
3. Specs live in `docs/spec/<topic>/`, state the current design as fact, and open with
   `Status: Draft|Stable · Built|Partial|Planned · date · one sentence`. A `Stable` spec still
   changes in place, but every behaviour-changing edit also gets an ADR.
4. Plans live in `docs/plans/YYYY-MM-DD-slug.md`, are committed on the feature branch, and are
   deleted when the feature lands after anything durable moves to an ADR, a spec or the
   nice-to-have ledger.
5. Every flow gets a Mermaid diagram in a fenced block. Text accompanies it, never replaces it.
6. Third-party behaviour is cited only from public documentation. Behaviour learned any
   other way is recorded as an assumption to verify, never as a fact with a hidden source.

## Consequences

- The rulebook and its rationale are separate files, so always-on context stays small.
- Behaviour learned from anything other than public docs is written down as an assumption to
  verify, never as a fact with a hidden source.
- The original product spec and the design conversation live in `docs/research/` as frozen
  provenance.
