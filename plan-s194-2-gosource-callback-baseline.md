# S194.2 — repair the Sprint 184 GoSource callback baseline

Story-ID 4e26e484961f (Sprint #194). Record of the remeasurement, the drift
reconciliation, and the change made.

## What "the Sprint 184 GoSource callback baseline" actually is

The ticket names a "Sprint 184 GoSource callback baseline". There is no such
commit pair in this repository's history: git `Sprint 184` is the unrelated
TypeScript project-runtime engine (`c8e3fcac..1054ee06`, "Sprint 184 TypeScript
engine story"). The GoSource callback baseline the ticket is about is the
general callback / signature / copied-slice bridge introduced in **Sprint #118,
Story #64 (`45be321bddfb`)** and hardened by the testing-callback provenance
correction in **Story #56 (`3ef468f4e831`)**. The interruption-teardown
lifecycle sits in Story #63 (`49c368b01841`). The remeasurement below is against
those, not against the TypeScript sprint the label points at.

## The "3/8-failure drift", reconciled

The "3/8" traces to `docs/evidence/sprint118-story56/README.md`, which reports
two different counts that later got conflated into a single "3 of 8":

- `TestTestingCallbackStatusProvenance` has **3 discriminating cases** that fail
  against the old blanket `status==1 -> pass` reduction and pass only against the
  provenance-keyed one (`unstamped status one`, `stamp superseded within the
  callback`, `stamp issued before this callback`).
- The ten-root pinned `errors_test` corpus replay had **3 pass / 7 fail** at that
  point, every failure landing ahead of any scheduler callback in the shared
  value model.

Neither is "3 of 8". The drift is a bookkeeping conflation, not a live test
regression: the provenance narrowing that the 3 discriminating cases demanded is
in `interp/gosource_testing.go` (`testingCallbackStatus`) today, and its unit
guard (`interp/gosource_testing_internal_test.go`) is green.

## Remeasurement of the current HEAD (`agent/weave-issue-223`)

Local runs, `PATH=/bin:/usr/bin:$(dirname $(which go))` to avoid the ycode `sh`
shim (see CLAUDE.md), `go1.27.0 darwin/arm64`:

- `go test -short -run 'TestGoSourceCallback' ./interp/` — **PASS**, `ok
  mvdan.cc/sh/v3/interp 21.539s`. All of `TestGoSourceCallbackEffects` (7
  subtests), `…CancelReset`, `…ReferenceBoundary` (3), `…BodyFailure`,
  `…UnsupportedBodyPropagation`, `…Signatures` (4), `…SignatureBoundary` passed.
- The new lifecycle/policy regressions below — **PASS**, `ok … 9.022s`.

Failing callback test names at HEAD: **none**. First error: **none**. The
baseline is green; there is no hidden red, no expected-failure list, no timeout
increase, and no fixture-identity special case involved in reaching green.

## Change made

Because the baseline measures green, the repair is to make the lifecycle and
policy invariants the drift touched **explicitly guarded from outside the pinned
corpus**, so a future regression cannot slip through unmeasured:

`interp/gosource_callback_lifecycle_test.go` (new, package `interp_test`) adds
three freshly authored — non-fixture — original programs:

1. `TestGoSourceCallbackReuseAfterRecoveredPanic` — a `String` method whose panic
   fmt recovers is reused successfully afterwards, twice interleaved. Pins that
   recovered-panic teardown leaves the borrowed caller state (result/call cells,
   panic/exit bits) clean. Measured against a real Go build of the same bytes.
2. `TestGoSourceCallbackInterleavedReceiverLifetimes` — pointer- and
   value-receiver `String` callbacks interleaved in a loop; the pointer receiver
   keeps mutating one shared original value, the value receiver is a fresh copy
   each call. Pins per-callback save/restore of in-flight argument state.
3. `TestGoSourceCallbackAsyncRetainedRefused` — `time.AfterFunc` (asynchronous
   retention) is refused before the dependency can keep the function and never
   runs out of band. Pins that the retained-callback refusal is a general policy,
   not the single `filepath.WalkDir` case already covered.

Cases 1–2 compare interpreter output byte-for-byte against the native oracle
(`differGoSource`), so their expectations are computed from real Go, never
hardcoded. Case 3 asserts the refusal and that the callback did not run.

## Gate

- Focused old/new comparison: callback suite + new regressions green (above).
- `gofmt -l` clean on the new file; `git diff --check` clean.
- Broad `go test -short ./...` is deliberately NOT run in this workspace: it is
  the integration manager's gate, run once after this change is merged. Nothing
  here claims the full suite has completed — only the focused callback tests and
  the format/diff checks above were run.
