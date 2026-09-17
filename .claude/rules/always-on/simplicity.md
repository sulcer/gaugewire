# Simplicity

> **Always-on rule.** Applies to every design, plan and code change.

The boring, direct solution that satisfies the requirement in front of you wins. Indirection
must earn its place: no interface with one implementation, no factory for one product, no
configuration knob for a constant, no scaffolding for later. Prefer the standard library, then a
platform feature, then a dependency already present, then new code. When a task seems to need
the complex version, present the simple and the complex versions with their trade-offs and let
the reviewer choose. Simplicity governs how, never whether: it never overrides correctness,
validation at a trust boundary, error handling that prevents data loss, or the gate.
