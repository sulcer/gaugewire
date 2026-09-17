---
paths:
  - "**/*_test.go"
  - "testdata/**"
  - "fixtures/**"
---

# Go tests

> **Path-scoped:** loads when you touch tests, test data or fixtures.

- Standard `testing` package. Table-driven when there are several cases. `t.Parallel()` in every
  test unless it mutates process state. `t.Context()`, `t.TempDir()`, `t.Setenv()`.
- One assertion per test: build the whole expected value and compare it once, with `==` for
  comparable structs or `cmp.Diff` otherwise. Never assert field by field.
- An expected value comes from the spec, a fixture, or arithmetic a reader can follow. It never
  comes from running the code under test and pasting what it printed.
- Golden files live in `testdata/` and are regenerated only with the diff shown to a human.
- Time-dependent logic uses `testing/synctest`. Network is `httptest`; no test contacts a real
  service. A test that spawns the built binary carries `//go:build integration`.
- A test name states the behaviour: `TestReduceKeepsStateWhenWindowAbsent`.
