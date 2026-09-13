# Bash++ interpreter per-call cost

Sprint 162 story #93, track B asks one decision question: can a general
interpreter mechanism make the 19 deadline roots fit the unchanged 60-second,
2-vCPU bound in this sprint? The measured answer is **not from frame pooling or
map replacement alone**. A non-recursive typed scalar instruction path has
enough best-case headroom to merit a separately gated implementation spike,
but it has not yet demonstrated product semantics or a leaf pass. Until it
does, the 19 roots remain FAIL by recorded decision.

The complete measurements are in
`docs/evidence/sprint162-percall-profile.md`. The selected upstream root is
`testdir:abi/fibish.go`, the smallest of the 19 deadline sources. The committed
outside-corpus equivalent is `interp/testdata/sprint162/percall/main.go`.

## Measured baseline

The outside-corpus `fib(25..30)` timings fit a 1.145-second startup plus
2.373 microseconds per interpreted call on Darwin with `GOMAXPROCS=2`.
`fib(40)` projects to 786.8 seconds, 13.1 times the 60-second bound. The
selected upstream root is costlier because it returns two values and uses two
short declarations; its earlier full calibration is roughly 22 times the
bound, and its new ten-second CPU/allocation profiles reproduce the same hot
mechanisms.

The one locked 2-vCPU leaf timing of the outside control at `n=30` was 30.62
seconds versus 7.58 seconds on Darwin, a 4.0396x ratio. The calibrated `n=40`
projection is therefore about 3,178 seconds, **52.97 times the leaf bound**.

At `n=25`, one outside-corpus run allocates about 660 MB in 11.78 million
allocations: 2.72 KB and 48.5 allocations per logical call. Approximately 25%
of bytes construct frames and environments; 55--63% copy or box scalar cells.
The generic syntax/statement call path accounts for 42% of the CPU profile
inclusive, while allocation/GC coordination is at least 44% by flat samples.
Defer/recover machinery is only 2.13%, and channel/goroutine setup is absent
from the recursive hot path.

The recursive Go stack remains about 14.5 KiB per interpreted level. Two
generic functions dominate: `Runner.cmd` reserves 5.31 KiB and
`Runner.stmtSync` 2.92 KiB. Heap pooling cannot help the two stack-exhaustion
roots; a non-recursive evaluator is required.

## Candidate 1: reuse frames and pool environments

Mechanism: turn `bashPPFrame`, `overlayEnviron`, and `bashPPScope` construction
into reusable objects, clear owned state on return, and retain strict
generation/lifetime checks so closures cannot observe a recycled scope. A
recursive call still needs one live frame per depth, so this reuses storage
across sibling calls but never aliases simultaneously live frames.

Likely files:

- `interp/bashpp_func.go` for frame entry/leave and the pool lifecycle;
- `interp/bashpp_scope.go` for typed-scope reset and closure escape checks;
- `interp/vars.go` for function-environment reset/release;
- focused tests under `interp/testdata/sprint162/` for recursion, closure
  escape, panic/unwind, and retained callback negatives.

Measured bound: the complete frame/environment bucket is about 675 bytes/call,
25% of allocation. Even the deliberately optimistic model where removing that
byte share removes the same time share yields only `1/(1-.25) = 1.33x`.
Allowing another 5% for frame bookkeeping gives a generous **less than 1.4x**
estimate. It leaves a 22x root about 15.7 times over the bound and does not
alter the 14.5 KiB recursive stack.

Number bought against the 19 roots: **0 proven; 0 conservatively projected**.
It may help one near-bound ordinary-call root, but there is no leaf evidence
that promotes that possibility to a count.

## Candidate 2: slice-indexed locals instead of maps

Mechanism: derive a stable slot layout for parameters, named results, and
lexical locals when a function is registered. A frame carries a `[]*bashPPCell`
indexed by slot; name maps remain only for dynamic shell variables, reflection,
diagnostics, and the generic fallback. Captured locals use stable cell handles,
not indices into storage that can move or be recycled.

Likely files:

- `interp/bashpp_func.go` for per-function layout and frame slots;
- `interp/bashpp_scope.go` for slot lookup plus the name-map fallback;
- `interp/bashpp_scalar.go` for direct identifier reads/writes;
- `interp/vars.go` only where a typed local must remain visible to shell
  assignment semantics.

Measured bound: typed-scope maps, parameter map entries, and string-map CPU
make up about 15% of allocation and roughly 4% of direct sampled CPU. Removing
all of that, with no replacement cost, gives an optimistic **1.05--1.2x**.
It is useful as an enabler for a direct call path and safe frame reuse, but is
not independently a deadline mechanism.

Number bought against the 19 roots: **0 proven; 0 conservatively projected**.

## Candidate 3: non-recursive direct scalar call path

Mechanism: compile an eligible Bash++ function's already-checked syntax once
into a small internal instruction sequence with typed scalar slots and explicit
interpreter frames. Calls push an instruction frame rather than recursively
entering `stmts -> stmt -> stmtSync -> cmd`; integer operands remain compact
typed values rather than copied `bashPPCell` graphs. Unsupported constructs,
dynamic shell interactions, dependency calls, and any guard failure fall back
to the existing evaluator before observable work begins. This remains
interpreted execution: the product executes its own instructions and never
runs tested source with the native Go toolchain.

Likely files:

- a new `interp/bashpp_scalarcode.go` for validation, instructions, and the
  explicit frame stack;
- `interp/bashpp_func.go` to cache eligibility/code and dispatch calls;
- `interp/bashpp_scalar.go` for compact typed scalar operations and conversion
  at the fast/generic boundary;
- `interp/bashpp_scope.go` for stable scalar slots;
- outside-corpus equivalence/negative tests for overflow, named types,
  multi-results, closures, defer/panic/recover, diagnostics, and fallback
  before any corpus leaf.

Measured/bounded estimate: a deliberately incomplete non-product scalar-stack
loop measured 1.746--1.773 ns per logical call with zero allocations. A real
path cannot claim that number. Leaf calibration leaves only about 41.4 ns/call
in Darwin prototype units, or **23.5 times** the toy median. At 20 times toy
overhead the leaf projection is 51.7 seconds, a 61.5x improvement over the
calibrated current projection; at 25 times it is 63.5 seconds and already
misses the bound; at 50 times it is 122.3 seconds. Thus the implementation
gate is explicit: the semantics-complete path must stay within about 23 times
this deliberately incomplete loop; a prototype result is not a pass.

Number bought against the 19 roots: **0 proven today; at most 3 conditionally
bounded** for the declared/closure/mutual-recursion fib family, and only if the
complete path stays within the measured 23.5x toy-overhead budget on the leaf.
The Peano/closure stack roots need the same explicit-frame architecture but use
richer values, so they are not included in the three. The arithmetic,
finalizer, concurrency, collection, and semantic hang roots need other
mechanisms and cannot be credited to this fast path.

## Candidate 4: compiled fallback

Mechanism: transpile and build with the pinned Go toolchain, then execute the
compiled artifact. The outside control's `go run` at `n=30` took 0.82 seconds
including tool startup versus 7.58 seconds interpreted; the selected upstream
root's recorded native run was 0.96 seconds versus a many-minute interpreted
projection.

Likely files are outside this design lane: the downstream CLI command routing,
the Bash++ backend coordinator, and the existing `gosource`/`lower` transpile
path. No such edit is requested here.

Number bought against the 19 roots: potentially **19 wall-clock completions,
0 interpreted credits**. D3 forbids calling native execution of tested source
an interpreted PASS. This is an operational mode choice, not a repair for the
interpreter, and it cannot close D2.

## Decision table

| candidate | estimated speed-up | stack exhaustion fixed? | roots credited now | roots conditionally bought |
|---|---:|---|---:|---:|
| frame/environment reuse | <1.4x | no | 0/19 | 0/19 |
| slice-indexed locals | 1.05--1.2x alone | no | 0/19 | 0/19 |
| direct scalar instruction path | 61.5x at 20x toy overhead; misses the leaf bound at 25x | yes for eligible code | 0/19 | at most 3/19 |
| compiled fallback | large, root-dependent | native stack only | 0/19 under D3 | 0/19 interpreted |

The counts are deliberately conservative. A deadline first line is not proof
that all 19 roots share the fib mechanism. Prior scaled controls already
separate finalizer/lifecycle hangs, bridge ownership errors, wide-integer and
collection costs, and goroutine/channel stress. Those rows must move to their
real seam or remain explicit design findings; no per-call optimization may
claim them without a reproducer and leaf verdict.

## Recommendation

Record D2 as **no deadline product code in the current repair wave and keep all
19 roots FAIL by ID**. Frame pooling and local-slot maps cannot buy the required
order of magnitude. The leaf-calibrated scalar path would need to remain within
about 23 times an intentionally incomplete toy loop after adding every
semantic obligation, so it is not an in-sprint repair. Authorize a later,
separately measured scalar-instruction spike only if it starts with
multi-result and closure-negative semantics and must demonstrate at least 22x
on the leaf before merge; otherwise stop it.
Do not use a compiled fallback for interpreted credit, and do not raise the
60-second timeout.
