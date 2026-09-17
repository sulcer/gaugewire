# Specs

The index of every spec in this repo. A spec states the **current** design as fact and is
edited to stay current, never frozen as a snapshot. Each spec carries two markers: a lifecycle
`Status` (`Draft` or `Stable`) and a build state (`Built`, `Partial`, `Planned`). The first says
how settled the design is, the second how much of it exists in the tree. `Stable · Planned` is
legal: a settled design nobody has built yet. See the
[documentation system ADR](../adr/2026-09-17-documentation-system.md).

| Spec | Status | Built | Scope |
|---|---|---|---|
| [`gaugewire`](gaugewire/README.md) | Draft | Partial | The product: capture Claude Code quota from the status line, normalize, persist, publish to sinks. Databox is the first sink. |
| [`repo-scaffold`](repo-scaffold/README.md) | Stable | Built | How the repository is built, gated, released and made legible to humans and agents. |

Once a spec is `Stable`, every behaviour-changing edit also gets an ADR and the changed section
carries a `Changes: <adr-file>` back-link. When something a spec describes gets built, its
marker flips in the same PR.

## Start here

New to the product: [`gaugewire/README.md`](gaugewire/README.md), then the
[glossary](gaugewire/glossary.md), then [architecture](gaugewire/architecture.md).

New to the repo: [`repo-scaffold/README.md`](repo-scaffold/README.md).
