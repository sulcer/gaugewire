# Gaugewire

Lightweight quota observability for a fleet of Claude Code machines. Gaugewire installs itself
as Claude Code's status-line command, reads the documented five-hour and seven-day quota fields,
keeps one durable state per machine, and publishes normalized events to a sink. Databox is the
first sink.

> Claude Code owns quota discovery. Gaugewire owns normalization, local durability and delivery.
> Sinks own storage and visualization.

## Status

Scaffold only. The `version` command exists; the observer is specified in
[`docs/spec/gaugewire/`](docs/spec/gaugewire/README.md) and arrives in later plans.

## Build

```sh
git clone git@github.com:sulcer/gaugewire.git
cd gaugewire
make build
./gaugewire version
```

Go 1.27 is required; the toolchain downloads itself.

## Develop

```sh
make setup      # git hooks and pinned tools
make check      # format check, vet, lint, unit tests
make ci         # everything CI runs
```

See [`CONTRIBUTING.md`](CONTRIBUTING.md) and the rulebook in [`AGENTS.md`](AGENTS.md).

## Documentation

[`docs/README.md`](docs/README.md) is the index: decisions in `docs/adr/`, the living design in
`docs/spec/`, procedures in `docs/how-tos/`.
