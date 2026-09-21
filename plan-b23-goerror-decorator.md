# B23 — `@go.error()`: decorator-generated typed value/error wrapper

Sprint 221, story 221.1 (`Story-ID d50392e49a40`, seq 572). Spec:
`dhnt/docs/sprint-221-master-execution-plan.md` §221.1; adapter phases pinned
by `docs/bashpp-adapter-contract.md` (Sprint 216).

## What ships

A language-claimed decorator `@go.error()` on a typed, receiver-less,
non-generic Bash# function declared in shell text. It adapts the EXISTING
Bash# value/error convention — declared results plus the call's completed exit
status — into Go's `(T, error)` convention at the decorated-call boundary:

- the declaration's public surface gains one trailing unnamed `error` result;
- the trailing error is non-nil exactly when the completed call's status is
  nonzero, with message `<name>: exit status <N>` (the `os.ProcessState`
  spelling);
- the call's status is UNCHANGED — `$?` still reports N, so classic status
  logic, `set -e`, contracts and attestation observe exactly what an
  undecorated call reports (status/contract preservation);
- rungs of the same chain still see the body's own results and Status; the
  conversion happens after the chain settles (`convert output` phase 4 of the
  frozen adapter order), so a decorator short-circuit (`Next` skipped,
  `c.Status = M`) also converts;
- a failed chain (`BASHPP-EDECO-*`), a propagating panic, and the agentic
  refusal keep their existing behavior — the adapter never swallows them
  (Bash# adds no exception handling);
- at lowering time the private entry and public symbol become an ordinary Go
  `(T..., error)` function — the generated wrapper the story names. The
  interpreter implements the same observable behavior through the existing
  `bashPPInvokeDecorated` settle; parity is behavioral, not source-shape.

`@go.error` takes no arguments, may appear once, and is refused on shell
functions, methods, generic functions, and functions already declaring a
trailing `error` (diagnostic `BASHPP-EDECO-GOERROR`, status 2, matching the
other registration diagnostics). Other namespaced decorators remain
`BASHPP-EDECO-RESERVED` — the namespace was reserved (Sprint 197) precisely so
the language could claim spellings like this one without breaking user code.

## Where it lives (no second evaluator/registry)

- `interp`: recognition + validation in `bashPPFuncDecl` /
  `bashPPRegisterDecorators`; the marker contributes NO rung
  (`bashPPDecoratorRungs` filters it); `bashPPFunc.results()` reports the
  adapted signature, `bodyResults()` the original; the error cell is minted in
  `bashPPInvokeDecorated` after the chain settles.
- `lower`: recognition + validation in `prepareDecorators`/`checkDecorators`;
  the synthetic `error` result field is appended to the declaration so every
  existing call-site/projection path types the adapted surface; the body still
  compiles against the original result list (`e.resultTypes`), the decorated
  closure keeps the body signature, and the entry settles the chain's N
  results then computes the trailing error from `Call.Status` via
  `shellrt.GoError`.
- `lower/shellrt`: `GoError(name, status)` + `GoErrorValue` (named string with
  `Error()`, so a non-nil `$err` interpolates its message and a nil one
  interpolates empty in both modes).

Known MVP limits (documented, deliberate): the adapter error is consumed as
`$err` text, nil-comparison, and preserved `$?`; `err.Error()` /
type-assertion on the minted value is not part of the MVP surface (structured
island errors are story 221.2 / B4). Methods and generics can be admitted
later through the same seam.

## Fixture provenance (source-derived, pinned)

| # | Upstream source | Commit / tag | Location | License | What it pins here |
|---|---|---|---|---|---|
| 1 | golang/go — `(T, error)` convention | tag `go1.25.0` | `src/builtin/builtin.go` (`error`), Effective Go §Errors | BSD-3-Clause | success → non-nil value + nil error; zero value + nil error is a SUCCESS, distinguishable from failure |
| 2 | golang/go — status→error message | tag `go1.25.0` | `src/os/exec_posix.go` `ProcessState.String` (`"exit status %d"`), `src/os/exec/exec.go` `ExitError` | BSD-3-Clause | the minted message `<name>: exit status <N>`; a nonzero status IS the error |
| 3 | this repo — decorator ordering/error tests | commit `47433440` | `lower/decorator_lowering_test.go` `TestDecoratedCallableExecution` rows "trace sees name and status", "skip denies the body…"; `lower/decorator_contract_test.go` "panic_defer"; `interp/bashpp_decorator_test.go` | BSD-3-Clause (mvdan.cc/sh fork, the bashy authors) | ordering (outermost first), short-circuit yields zero results + decorator status, panic/defer and status propagation the adapter must preserve |

Coverage required by the story → fixture names (in
`lower/goerror_decorator_test.go`, run through the compile+interpret parity
harness, and `interp/bashpp_goerror_test.go` for registration diagnostics):

- value success → `value_success`
- zero/empty value → `zero_value_success`
- error (nonzero status) → `status_error`, `status_error_preserves_status`
- decorator short-circuit → `short_circuit_converts`
- panic/status preservation → `panic_propagates`, `status_error_preserves_status`
- ordering with user rungs → `rungs_see_body_results`
- refusals → interp test table (args, twice, method, shell function, generics,
  existing trailing error, other namespaced names still reserved)
- infrastructure failure (Decorate/DecoratedResults/DecoratedResult false) is a
  hard failure, NOT a minted `(zero, error)` → `TestGoErrorDecoratorInfrastructureFailure`
  (`undefined_native`, `invalid_args_rewrite`, `invalid_results_count`,
  `invalid_results_type`), plus `TestGoErrorDecoratorFailureReturnsZeroError`
  guarding the lowered shape (failure branch returns the zero error; GoError is
  emitted only on success paths)
- 8-bit status wrap (0, 255, out-of-range 256/257) agrees across engines →
  `TestGoErrorDecoratorStatusBoundary` (lower parity) and
  `TestBashPPGoErrorDecoratorStatusBoundary` (interp); `shellrt.NormalStatus`
  and `shellrt.GoError` normalize consistently, unit-pinned by `TestNormalStatus`
  / `TestGoErrorNormalizes`
- nil-interface interpolation renders empty, matching the interpreter →
  `shellrt.TestWord` (nil interface / nil error render empty; typed nil pointer
  keeps `<nil>`)

## Gates

- `go test ./interp -run TestBashPPGoError` (quick tier)
- `go test -tags full ./lower -run TestGoErrorDecorator` (parity: compiled
  output diffed byte-for-byte against the interpreter, statuses compared)
- `go test -tags full ./lower -run TestDecoratedCallableExecution` and
  `go test -tags full ./interp -run 'TestBashPPDecorator'` (existing seam
  unchanged)
- `go test -tags full ./lower ./interp ./polyglot` (story gate)
- Bash-OFF/classic: decorators only parse under `LangBashPP`
  (`TestBashPPDecoratorDialectIsolation` already pins this; unchanged)
