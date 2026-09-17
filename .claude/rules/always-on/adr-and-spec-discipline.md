# ADR and spec discipline

> **Always-on rule.** Applies to every behaviour-changing change and every documentation edit.

## ADRs

A decision someone would otherwise re-litigate gets `docs/adr/YYYY-MM-DD-kebab-title.md`, with
frontmatter `tags`, `status: accepted|superseded`, `decision-date`, and these sections in order:
Title, Members, Status, Context and Problem Statement, Options considered, Decision,
Consequences, and optionally Out of scope. Every section earns its place or gets one line. Add a
row to `docs/adr/README.md`. Never delete an ADR. To supersede, set the old file's
`status: superseded` and add `Superseded by: <file>` right after its title, and put
`Supersedes: <file>` in the same position in the new one. When only some decisions change, amend
instead: `Amended by: <file> (decision N)` on the old, `Amends: <file> (decision N)` on the new,
and say explicitly which decisions still stand.

## Specs

`docs/spec/<topic>/` states the current design as fact. Every page opens with
`Status: Draft|Stable · Built|Partial|Planned · date · one sentence`. The build state is a claim
about the tree: check it before writing it, and flip it in the pull request that builds the
thing. A `Stable` spec is still edited in place, but every behaviour-changing edit also gets an
ADR and a `Changes: <adr-file>` back-link. Every flow gets a Mermaid diagram; prose accompanies
the diagram and never replaces it.

## Plans

`docs/plans/YYYY-MM-DD-slug.md`, committed on the feature branch, kept current as pull requests
land, and deleted in the final pull request of the feature, after anything durable has moved to
an ADR, a spec or `docs/nice-to-have.md`.
