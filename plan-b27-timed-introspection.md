# B27 — `@timed` and Bash# value introspection through `define`

Sprint 221, story 221.9 (`Story-ID c19cb824e6bd`, seq 580). Spec:
`dhnt/docs/sprint-221-master-execution-plan.md` §221.9; adapter phases pinned
by `docs/bashpp-adapter-contract.md` (Sprint 216). Two commits, two gates:
B27a first, B27b second, exactly as the story orders them.

## B27a — `@timed`: engine-supplied observation decorator

A built-in native decorator named `timed`. It is an ordinary rung of the
existing chain — the behavior/observation adapter of the frozen phase order —
not a marker like `@go.error`: it runs `Next` exactly once, measures the
monotonic wall time of everything inside it, and appends one stable
attestation line to standard error after the inner chain settles:

```
@timed: <name>: status=<N> duration=<D>
```

`<name>` is `Call.Name`, `<N>` the settled `Call.Status`, `<D>` a Go
`time.Duration` string. The line is emitted from a deferred observation, so an
erroring body (nonzero status) and an unwinding panic are timed exactly like a
success — the Python try/finally timing-wrapper behavior. `@timed` never
rewrites `Args`, `Results` or `Status`: results, `$?`, contracts and every
other rung observe exactly what an untimed call reports.

Resolution order is the chain's existing one, unchanged: a script-declared
typed decorator named `timed` shadows the built-in (both engines), an
embedder native registered under `timed` (`interp.Decorators` /
`shellrt.Decorators`) overrides the built-in, a shell function named `timed`
stays `BASHPP-EDECO-SIG`, and only when all of those miss does the engine
supply the built-in — so the built-in occupies the slot `EDECO-UNDEF`
previously reported and can never take a name from user code. `@timed` takes
no arguments; an argument list is refused through the existing native path
(`BASHPP-EDECO-NATIVE: @timed: requires no arguments`).

Where it lives:

- `interp/bashpp_timed.go`: `bashPPTimedDecorator` (a `DecoratorFunc` closure
  over the runner's stderr); `bashpp_decorator.go` resolves it as the last
  rung fallback before `EDECO-UNDEF`.
- `lower/shellrt/timed.go`: the same closure over the program's stderr;
  `decorator.go`'s `next` falls back to it when `Decorators` misses. No
  compiler change: a `timed` rung lowers as an ordinary native rung.

Interpreted/lowered parity is behavioral and measured: the parity harness
diffs both engines byte for byte after normalizing only the `duration=` value,
which is real measured time and legitimately differs per run.

## B27b — session value type and fields through `define`

`bashy define <var>` (the umbrella's one "what is this name" surface) gains
the PowerShell `Get-Member` answer for a live Bash# session value. The engine
side — this repo — is one bounded, read-only introspection API; the `define`
wiring is the bashy repo's consumer change:

- `interp.ValueDescription` / `interp.ValueField` and
  `Runner.DescribeValue(name string) (ValueDescription, bool)` — the
  `Runner.LiveVar` pattern extended to typed cells: name → declared Go-spelled
  type, kind (`scalar`, `record`, `list`, `map`, `handle`, `nil`), rendered
  scalar value, element count, and a record's declared fields in order.
- `HandlerContext.DescribeValue` delegates, so an in-process `define`
  command reaches the same answer mid-run.

Boundaries (the story's "not an evaluator or debugger"):

- Input is one plain variable name. Selectors, indexes and expressions are
  refused (`false`), not evaluated.
- Reads only: no method call, no conversion, no cell mutation, no dereference
  chase. A pointer, channel, function value or other handle reports its type
  and nil-ness with the value withheld (`Redacted`) — identity is the answer.
- Redaction: a record's private (lowercase) fields are listed by name and type
  with the value withheld; opaque field values (channel/func/pointer) are
  withheld the same way. There is no `-Force` counterpart — nothing reveals
  them.
- A classic shell variable answers through the same API (`string`,
  `[]string`, `map[string]string`), so `define` needs one call, not two.

## Fixture provenance (source-derived, pinned)

| # | Upstream source | Commit / tag | Location | License | What it pins here |
|---|---|---|---|---|---|
| 1 | python/cpython — timing wrapper | tag `v3.12.0` | `Lib/timeit.py` (`default_timer = time.perf_counter`; `Timer.timeit` restores GC state in `finally`) | PSF-2.0 | the timer is monotonic; observation wraps the call without changing its outcome; the finally-shaped emission times an erroring call exactly like a success |
| 2 | PowerShell/PowerShell — member inspection | tag `v7.4.0` | `src/Microsoft.PowerShell.Commands.Utility/commands/utility/GetMember.cs` (`Get-Member`) | MIT | inspection lists member name/type/definition without invoking or mutating the object; hidden members stay hidden by default |
| 3 | this repo — decorator ordering/adapter tests | commit `53a95043` | `lower/decorator_lowering_test.go` `TestDecoratedCallableExecution`; `lower/goerror_decorator_test.go`; `interp/bashpp_decorator_test.go` | BSD-3-Clause (mvdan.cc/sh fork, the bashy authors) | rung ordering (outermost first), short-circuit, status preservation, and the settle order (`invoke → convert output`) the observation rung must not disturb |

Coverage required by the story → fixtures:

- success timing → `success_timing` (parity) + `TestBashPPTimedDecorator/success`
- error timing → `error_timing` (parity; nonzero settled status on the line,
  `$?` preserved) + `TestBashPPTimedDecorator/error_status`
- nested decorator order → `nested_user_rung`, `stacked_timed` (parity)
- value passthrough (results unchanged) → `value_passthrough`, and
  `goerror_composition` (@timed beside the B23 adapter)
- shadowing/refusals → `shadowed_by_script` (parity),
  `TestBashPPTimedDecoratorResolution` (embedder override, shell-function
  EDECO-SIG, argument refusal)
- scalar/record/list/handle inspection → `TestDescribeValue` rows
- nil values → nil pointer, nil interface rows
- private/opaque redaction → `secret` field row, channel-field row, handle
  rows
- not-an-evaluator boundary → selector/index/expression names refused

## Gates

- B27a: `go test ./interp -run TestBashPPTimed` (quick tier);
  `go test -tags full ./lower -run TestTimedDecorator` (parity);
  `go test -tags full ./lower -run TestDecoratedCallableExecution` and
  `go test -tags full ./interp -run TestBashPPDecorator` (existing seam
  unchanged).
- B27b: `go test ./interp -run TestDescribeValue` (quick tier).
- Story gate (both commits): `go test -tags full ./interp ./lower`.
- Bash-OFF/classic: decorators parse only under `LangBashPP`, and
  `DescribeValue` reads state only a Bash# session creates; classic dialects
  answer through the shell-variable fallback without any Bash# machinery.
