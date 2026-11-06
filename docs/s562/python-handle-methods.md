# S562 B3 batch 4 — Python fence handle methods

Sprint #562, story #3040, Story-ID 6a4f4591bc8e. Implements the four
manager-approved steps in `go-internal-python-handles.md`.

## Implementation

Dynamic fence results retain an authenticated runtime `*polyglot.Handle` in
the first result cell and keep the error in the second position. The cell's
host assignment type is `any`; Python Type/Repr metadata grants no authority.
The existing selector dispatcher remains responsible for handle calls.

Handle calls retain their one-value form and accept an explicit value/error
binding or reassignment. Existing foreign error classification puts Python
exceptions into the error result; transport and cancellation remain failures.
Method-returned handles retain the same carrier and can receive further calls.

Lowering propagates possible Python values through first-result declarations
and assignments to a fixed point. Typed local selectors take precedence over
name-based Python provenance. Generated helpers check the actual runtime
handle before dispatch, preserve output before returning errors, use the
program context where available, and support result frames. Fence-only units
emit the same helpers as direct imports. No worker protocol, method discovery,
iterator or callback behavior changed.

## Regression evidence

Before implementation, the shared make/read fixture failed in the interpreter
with `BASHPP-ESELECTOR-TYPE: local h has no typed selector path` and tuple
assignment errors. Lowering also rejected the fixture. An added typed-shadow
case subsequently exposed Python dispatch incorrectly taking precedence over
a typed parameter in lowering; its fix is covered by the final shared fixture.

The shared fixture asserts exact output for StringIO make/read, one-value
compatibility, scalar method results, method-returned handle chains, two-result
declarations and reassignments, assignment into declared `any`, Python
exceptions, missing and non-callable attributes, stdout/stderr, and typed
selector precedence. Interpreter and native output must match. Separate
negative tests reject handle-looking strings and keep worker death out of the
opt-in domain error. Runtime tests exercise cross-module rejection, release,
and stale handles after cancellation. Existing direct-import tests remain in
the requested gate.

## Verification

* `go test ./polyglot/...`: PASS (48.356s).
* `go test -tags full ./interp -run 'BashPP|Foreign|Polyglot|Fence|Island|Python' -count=1`:
  PASS (239.862s). The known FIFO/procsub timeout did not occur; no retry.
* `go test -tags full ./lower -run 'Polyglot|Fence|Python' -count=1`:
  FAIL (220.169s), only in the existing
  `TestForeignErrorOptInPython/worker_death_stays_infrastructure/{static,execution}`
  cases. They expect status 1; the interpreter preserves worker status 3.
  The error binding remains empty and the diagnostic identifies worker death.
* Batch-4 parent comparison: a Go overlay restored all seven changed production
  files from HEAD without changing this working tree. The two existing
  worker-death subtests failed identically (7.639s). This status-expectation
  mismatch predates batch 4, but was introduced by S562 batch 2. This was
  not a comparison against the true pre-S562 baseline.
  The new handle cases passed in the full lowering selection.
* Follow-up correction: the manager bisected the Python regression to batch 2
  (`8bb18fd0`, implementation `31382d3c`); `6663c93a` and `9f3f81b6` pass.
  `WorkerExit.ExitStatus` now preserves nonzero Go fence exits only. Other
  worker deaths retain infrastructure status 1 and an empty opt-in error.
  Both interpreter call paths and lowered wrappers use this same rule.
  Added Go exit parity cases cover plain calls and error bindings in static
  and execution lowering. Lowered Go exit propagation was also missing on
  `6663c93a` (confirmed with a value-returning Go function to avoid that
  baseline's separate void-export panic); the shared rule closes that gap.
* Stronger handle transport assertions exposed a second gap: static lowering
  panicked on worker death instead of returning infrastructure status 1.
  This path became reachable with batch 4; `6663c93a` rejects the same handle
  selector at compilation. The two-result helper now uses the calling
  program's status setter, reports worker death, and leaves the error empty.
  Both lowering modes are covered; other non-domain aborts remain aborts.
* New focused handle-method, transport-failure, and scalar-not-handle checks:
  PASS. No Bash 5.3 suite was run.

## Batch-4 landing and worker-status follow-up verification

All commands used `GOWORK=off` on this workspace. Batch 4 was cherry-picked
as `217b770b`; the follow-up retains the original Go exit tests.

* `go test ./polyglot/...`: PASS (83.933s).
* `go test -tags full ./interp -run 'BashPP|Foreign|Polyglot|Fence|Island|Python' -count=1`:
  PASS (414.824s).
* Focused Python handle tests, including stronger transport assertions in
  both lowering modes: PASS (17.792s).
* `go test -tags full ./lower -count=1 -timeout=30m`: FAIL (1501.882s).
  The sole reported failure is `TestS243UnchangedIssue43164`: the installed
  Go 1.27.2 toolchain lacks `test/fixedbugs/issue43164.dir/a.go`. The extended
  timeout follows this repository's full-suite guidance. Go exit parity,
  Python worker-death, and handle tests reported no failures.
* Baseline endpoint check: the same S243 test fails identically on an archive
  of `6663c93a` (0.438s); its source is unchanged between that baseline and
  this delivery. No change was made to this unrelated environment failure.
* Additional `TestRunnerRunConfirm`: FAIL only in `/0049`, `/0159`, `/0856`
  against Homebrew Bash 5.3.15 (printf padding, time-format `%q`, and the
  diagnostic from writing with closed stdout/stderr). All three reproduce
  identically at `6663c93a`; left unchanged.
