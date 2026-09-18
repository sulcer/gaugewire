# Why these rules

Companion to `AGENTS.md` and `.claude/rules/`. The rules say what to do; this file says why, so
the reasoning stays out of always-on context and is read only when someone asks "why is it like
this?".

## Identical shapes regardless of author

Reviews that argue about shape are reviews that miss logic. When every contributor, human or
agent, produces the same package layout, error style, test style and doc style, review time goes
to what the code does. This is the load-bearing rule; every other rule exists to serve it.

## Mechanical gates are hooks, not prose

A rule that says "run the formatter before committing" is followed most of the time. A hook that
runs the formatter is followed every time. Prose rules are reserved for decisions that need
judgment: whether a change deserves an ADR, whether a dependency earns its place, whether a
simplification cuts a corner. Everything a linter, a git hook or a Claude Code hook can check is
checked there, and the rule files stay short enough to be read on every session.

## Public documentation only

A third-party service's behaviour is relied on only when its public documentation states it. A
reader with nothing but the public docs must be able to check every claim in this repo, and a
rule built on undocumented behaviour breaks the day that behaviour changes. Anything learned
another way is written down as an assumption to verify, never as a fact.

## The ADR bar

An ADR records a decision someone would otherwise re-litigate: a choice with real alternatives
and consequences. A field rename is a commit message. Too many ADRs bury the ones that matter,
too few turn "why is it like this?" into archaeology. When only some decisions of an older ADR
change, the new ADR amends it and the old one gets a back-link, because an amended ADR without a
back-link reads as fully current.

## Specs are living, plans are ephemeral

A spec states the current design as fact and is edited in place; once stable, every
behaviour-changing edit also gets an ADR. A plan is scaffolding for turning a design into code;
it is committed so it survives branch switches, and deleted when the work lands, after anything
durable in it has moved to an ADR, a spec or the nice-to-have ledger. A lingering plan reads as
current work.

## Complete-object assertions and expectation provenance

Asserting three fields of a struct misses the fourth one that was added with the wrong value and
the fifth one that disappeared. Comparing the whole value catches drift in both directions. An
expected value is only meaningful when it comes from the spec, a fixture or arithmetic a reader
can follow; a value pasted from the code under test agrees with the code by construction and
proves nothing. Golden files are allowed for CLI output for the same reason they are dangerous:
regenerating them is one flag away, so an agent regenerates only with the diff shown to a human.

## Simplicity

The boring solution is usually right. Indirection has to earn its place: an interface with one
implementation, a factory for one product, a config knob for a constant are complexity paid by
every future reader. When a task seems to need the complex version, the simple and the complex
versions are presented side by side before the complex one is built.

## Standard library first

Every dependency is code nobody in this repo reviews, a supply-chain surface, and a version to
keep current. The standard library covers flags, JSON, HTTP, logging, UUIDs and testing. The two
exceptions, cross-platform file locking and whole-struct diffs in tests, each replace code that
is easy to get subtly wrong.

## JSON v1 API, not v2

The v2 JSON engine is stricter: case-sensitive field names, duplicate keys rejected. That is
right for our own files and wrong for a user-authored `settings.json` that Gaugewire must read,
edit and write back without surprising the user. One API for both keeps the behaviour uniform.
Go 1.27 runs the v1 API on the v2 engine anyway.

## Mermaid, not hand-drawn SVG

A flow deserves a picture, and a picture that lives as text is one an agent can author, review
and diff, and one that cannot go stale silently in a binary blob. GitHub renders Mermaid inline.

## Context only where something can block

A `context.Context` is required for a network call and for a wait on another process, such as an
advisory file lock, because either can hang and only a context gives the caller a way to time it
out or cancel it. A local file read or write is neither: it returns quickly or fails outright, so
threading a context through it adds a parameter nothing ever cancels.
