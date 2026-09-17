---
paths:
  - "go.mod"
  - "go.sum"
  - "tools/**"
---

# Dependencies

> **Path-scoped:** loads when you touch a module file.

- Standard library first. The allowed runtime dependency is `github.com/gofrs/flock`; the
  allowed test dependency is `github.com/google/go-cmp`. Anything else needs an ADR that names
  the standard-library alternative and why it does not do.
- Pin exact versions. Run `go tool -modfile=tools/go.mod govulncheck ./...` after any change and
  state the result in the pull request.
- Dev tools live only in `tools/go.mod` as `tool` directives, never as `go install ...@latest`.
- Dependabot cooldowns are configured in `.github/dependabot.yml`; do not merge a bump early.
- `go mod tidy -diff` must be clean for both modules.
