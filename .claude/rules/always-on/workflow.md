# Workflow

> **Always-on rule.** How to work in this repository.

## Read first

`AGENTS.md`, then the `docs/spec/` page for the area you touch, then any ADR it links. Do not
read `docs/research/`, `dist/` or `coverage.out`.

## Ask or proceed

Proceed when a rule, a spec page or an existing sibling answers the question. Ask when two rules
conflict, when a spec is silent about a contract, when a new dependency seems necessary, or
before any destructive or outward-facing action: pushing, releasing, deleting outside the tree.
If a rule is wrong, propose an ADR rather than breaking it quietly.

## Gate

Test-driven: write the failing test, watch it fail, make it pass, run `make check`. Every commit
passes the pre-commit hook; never bypass it with `--no-verify`. Commits and pushes happen only
with the owner's approval, unless autonomous mode was granted for that session.

## Boundaries

- Third-party behaviour is relied on only when its public documentation states it. Anything
  learned another way is written down as an assumption to verify, never as a fact.
- Never read `.env*` or `*.key`. Never log or persist a credential.
- Never modify `~/.claude/settings.json` except through `gaugewire install` and `uninstall`.

## Subagents

A subagent does not inherit these rules. Give it the rule files whose `paths` match what it will
touch, plus the relevant spec page. Verify its work yourself; verification is not delegated.

Rationale: [docs/why-these-rules.md](../../../docs/why-these-rules.md)
