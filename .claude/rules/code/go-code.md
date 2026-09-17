---
paths:
  - "cmd/**/*.go"
  - "internal/**/*.go"
---

# Go code

> **Path-scoped:** loads when you touch Go source.

- Errors: `fmt.Errorf("...: %w", err)`; a sentinel is `var ErrX = errors.New(...)` in the owning
  package; match with `errors.Is` and `errors.AsType`. No panic outside `main`. No `os.Exit`
  outside `cmd/gaugewire/main.go`; commands return errors.
- `context.Context` is the first parameter of anything doing I/O, and every network call has a
  timeout.
- Logging is `log/slog` only. A command writes to the `io.Writer` it was given, never to
  package-level stdout.
- Package layout and dependency direction are in `AGENTS.md`. Platform differences live in
  build-tagged files, `_unix.go` and `_windows.go`.
- Names follow Effective Go: short receivers, no stutter, every exported identifier documented
  with a sentence that starts with its name. No acronym a reader would have to look up.
- Comments explain why. They never narrate the diff, the history, or an issue number.
- Every file write is temporary file, fsync, rename. Every lock is an advisory file lock.
