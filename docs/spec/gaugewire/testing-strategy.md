# Testing strategy

Status: Draft · Partial · 2026-09-19 · What proves each package, how integration tests run, and what the acceptance test on a real machine must show.

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
| `store` | atomic write recovery from a stray temp file and a truncated `state.json`; lock timeout fails open; spool ordering by name; dead-letter move and requeue; dead letters listed newest first with `.unreadable` files counted, never decoded; N goroutines running the hot path against one temp home produce exactly one publish |
| `settings` | fixture-driven goldens for `Get`/`Set`/`Delete` on a top-level member at every position (`empty`, `none` — no such member, `only`, `first`, `middle`, `last`, `compact`), each proved byte-exact against a `testdata/*.golden` file; the `last` fixture's CRLF and tabs kept as binary via `.gitattributes` so a checkout never rewrites its line endings; round trips (`Set` then `Delete` restores a file with no member; `Set` back to the original value restores a file that had one); malformed JSON and a non-object top level rejected |
| `renderer` | byte-for-byte stdin passthrough; stdout passthrough including ANSI; renderer failure does not fail the observer; observer failure does not blank the renderer |
| `sink` | router fans out to enabled sinks only; per-sink delivery bookkeeping; a sink added later is not targeted by older events; chunking at 100; a setup error fails the run and is recorded in `lastFlush`; the ingestion record: round trip, a corrupt state file reported as `ErrIngestionRecordCorrupt`, a held `state.lock` reported as retryable |
| `sink/databox` | a scripted `httptest` fake server exercises every request the client makes: the documented headers, the request bodies, 401 without leaking the key, an undecodable 2xx and a missing `ingestionId` retried, redirects not followed and permanent, plain http refused except to loopback, ids escaped in the path, and more than 100 records refused before any request; the 429, 5xx and 400 outcomes are covered by the `classify` table over every documented HTTP status; History and Current record shapes proved against `testdata/*.golden`; the sink itself: History then Current, Current skipped when not strictly newer than `currentCapturedAt`, ingestion ids recorded, the record loaded before posting (a locked record retries with no request, a corrupt one still sends Current), a Current failure saves nothing, and delivery still succeeds when the record cannot be saved |
| `config` | load, validate, unknown sink type, duration parsing, credentials precedence |
| `cli` | install and uninstall round-trip on a temp `settings.json` preserving unrelated bytes; refuse double install; warn on a modified status line; `status` and `doctor` golden output; `runFlush` end to end against `httptest` delivers to Databox and clears the spool, dead-letters an invalid key without logging it, retries a rate limit, fails the run for a sink with no key or no dataset ids; `doctor`'s sink rows, skipped when `config.json` is invalid, with a key-file warning on a valid key; `databox bootstrap` creates everything on an empty account, reuses existing resources, lists the accounts and saves nothing when several are reachable without `--account-id`, refuses a created resource without an id, records the key file path, sends the `--test-ingest` heartbeat and skips it before any observation |

Built so far: the `quota`, `source/claude`, `store`, `settings`, `config`, `renderer`, `sink`,
`sink/databox` and `cli` rows for every command that exists; integration tests for the parallel
hot path, the detached flusher, the install round trip, the doctor exit code and a flush through
the built binary against a fake API.

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
- `install` splices `statusLine` in a temp settings file, `statusline` observes against it,
  `doctor` reports healthy, and `uninstall` restores the file byte for byte.
- `doctor` exits 1 against a `config.json` with no install record (the "status-line integration"
  check fails as `not installed`).
- `gaugewire flush` through the built binary delivers to an `httptest` fake API and clears the
  spool.

Integration tests build the binary and prove that the detached flusher outlives the hot path,
that eight parallel status-line invocations publish once, that install and uninstall round-trip a
settings file, that `doctor`'s exit code reflects its verdict, and that a flush through the built
binary reaches a fake Databox API.

## Coverage

Hard gate on the pure packages `internal/quota` and `internal/source/claude`; reported for the
rest. See the [repo scaffold spec](../repo-scaffold/README.md).

## Acceptance test

Performed on a real Pro or Max machine before v1 is called complete:
[how-to](../../how-tos/acceptance-test.md).

## Open questions

None.
