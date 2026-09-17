# Contributing

1. Read [`AGENTS.md`](AGENTS.md). It is the rulebook for humans and agents alike.
2. Run `make setup` once per machine. It installs the git hooks and warms the pinned tools.
3. Work on a branch named `<type>/<topic>`. Commits are Conventional Commits without a scope.
4. Run `make check` before every commit; the pre-commit hook runs its format, vet and lint steps
   anyway. Run `make ci` before opening a pull request.
5. A behaviour change gets an ADR in `docs/adr/`. A spec page that now describes built behaviour
   flips its marker in the same pull request.
6. Open the pull request with the template. `main` moves only through pull requests.
