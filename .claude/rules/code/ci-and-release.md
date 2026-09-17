---
paths:
  - ".github/**"
  - ".goreleaser.yaml"
  - "Makefile"
  - "githooks/**"
  - "scripts/**"
---

# CI, hooks and release

> **Path-scoped:** loads when you touch automation.

- Every gate is one command: the Makefile wraps it, the git hook runs it, CI runs it. Never add
  a check in one place only.
- CI calls `go` directly, because Windows runners may lack `make`.
- Actions are pinned by commit SHA with the version in a trailing comment. Every workflow
  declares the least permission it needs.
- Hook scripts are POSIX `sh` without `jq` or `python`, so they run under Git for Windows.
- Releases are `v*` tags; goreleaser builds five binaries. Before 1.0, a breaking change bumps
  the minor version. The changelog is built from commit types, so keep subjects meaningful.
