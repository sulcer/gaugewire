# Testing strategy

Status: Draft · Planned · 2026-09-17 · What proves each package, how integration tests run, and what the acceptance test on a real machine must show.

## At a glance

Test-driven development, standard `testing` package, table tests, `t.Parallel()` by default,
whole-struct comparisons, expected values that come from the spec or fixtures and never from
running the code under test. No unit test touches the network; the Databox client is tested
against `httptest`. Tests that spawn the built binary carry the `integration` build tag. The
real Databox service is touched only by the manual acceptance test.

## Unit matrix

| Package | Must cover |
|---|---|
| `quota` | rate limits absent; 5h only; 7d only; both; 0 preserved; 100 preserved; decimals; malformed percentage; malformed reset; new session with missing window keeps unexpired state; reset passed and window missing → expired; expired then fresh → observed; staleness guard: same reset lower value ignored, same reset higher accepted, newer reset lower value accepted, older reset ignored; threshold accumulation against last published; reset change publishes; status change publishes; heartbeat; both unknown never publishes |
| `source/claude` | every fixture in `fixtures/statusline/`; missing and old `version`; unrelated fields ignored and never retained; fuzz test on the parser |
| `store` | atomic write recovery from a stray temp file and a truncated `state.json`; lock timeout fails open; spool ordering by name; dead-letter move and requeue; N goroutines running the hot path against one temp home produce exactly one publish |
| `renderer` | byte-for-byte stdin passthrough; stdout passthrough including ANSI; renderer failure does not fail the observer; observer failure does not blank the renderer |
| `sink` | router fans out to enabled sinks only; per-sink delivery bookkeeping; a sink added later is not targeted by older events |
| `sink/databox` (`httptest`) | happy path; 401; 429 with and without JSON body; 5xx; 400; missing `ingestionId`; chunking at 100; Current gets the newest; Current skipped when older than `currentCapturedAt`; bootstrap reuse by title and creation of only what is missing |
| `config` | load, validate, unknown sink type, duration parsing, credentials precedence |
| `cli` | install and uninstall round-trip on a temp `settings.json` preserving unrelated bytes; refuse double install; warn on a modified status line; `status` and `doctor` golden output |

Timing-dependent logic (backoff, heartbeat) uses `testing/synctest`. Tests use `t.Context()`,
`t.TempDir()` and `t.Setenv()`. Golden files live in `testdata/` and are regenerated only with
the diff shown to a human.

## Integration tests (`//go:build integration`)

- The built binary invoked N times in parallel with real fixtures against one temp home
  produces one state and one publish.
- The detached flusher is spawned, survives the parent exiting, takes `flush.lock`, and delivers
  to an `httptest` server; a second flusher exits immediately.
- End to end with a fake renderer script: Claude-shaped stdin in, renderer output out, event
  spooled, delivered.

## Coverage

Hard gate on the pure packages `internal/quota` and `internal/source/claude`; reported for the
rest. See the [repo scaffold spec](../repo-scaffold/README.md).

## Acceptance test

Performed on a real Pro or Max machine before v1 is called complete:
[how-to](../../how-tos/acceptance-test.md).

## Open questions

None.
