# Sprint 270 G2 interpreter profile

Story #757 (`609c89bfa598`) keeps the corpus timeout at 60 seconds and does
not use native execution for interpreted credit.

## Representative profile

Measurements used `GOMAXPROCS=2` on Darwin/arm64 and the existing
`BenchmarkSprint153Deadline` / `BenchmarkSprint153File` interpreter-only
benchmarks. The checked-in `abi_fibish/size25` control is the bounded
representative for `testdir:abi/fibish.go`.

Before this change, three size-25 runs took 2.048, 1.643, and 1.773 seconds
(median 1.773 seconds), with about 1.043 GB and 10.824 million allocations per
run. The allocation profile was dominated by copied assignment cells (43%),
short-declaration snapshots (13%), value cells (12%), and lexical declarations
(7%). After the eligibility-checked scalar-int plan, three runs took 0.870,
0.837, and 0.982 seconds (median 0.870 seconds), with about 3.0 MB and 16.5
thousand allocations. This is 2.04x by wall time at size 25, including the
roughly 0.8-second fixed Go-source startup, and more than 99% fewer allocated
bytes. The unchanged full `testdir:abi/fibish.go` source completed interpreted
in 8.21 seconds with a 60-second `go test` bound, 3.67 MB, and 17.3 thousand
allocations.

The `testdir:fixedbugs/issue11256.go` profile showed a separate manifestation
of repeated generic request preparation: 8.73 GB (61% of allocated bytes) was
spent copying environment strings before native comparisons which were then
resolved locally as scalar equality. Moving request construction after the
local scalar comparison reduced three interpreter-only runs from the measured
16.44-second, 15.38-GB baseline to 13.44, 13.74, and 13.61 seconds (median
13.61 seconds), about 1.21x faster and 45% fewer allocated bytes (8.23--8.45
GB). The exact source completes inside the unchanged local 60-second bound.

## Semantic boundary

The scalar plan accepts a complete function or none of it. It is limited to
side-effect-free, receiver-less, non-generic, undecorated Go-source functions
whose parameters and results are `int`, and whose body consists only of local
integer expressions, returns, conditionals, and self calls. Unsupported nodes
fall back before body execution. Normal argument, channel, interface, and
`FUNCNEST` gates run first; cancellation remains polled during execution.

The comparison change only postpones construction of bridge request state.
Non-scalar comparisons construct the identical request and use the existing
bridge path.

## Remaining verification

The pinned Linux do1 two-root leaf and the full 34-mode G2 replay remain
unverified in this isolated workspace. Apart from the two roots measured
above, the unverified G2 modes are:

- `package:cmd/compile/internal/test` interpreted
- `testdir:64bit.go` interpreted
- `testdir:abi/fibish_closure.go` interpreted
- `testdir:abi/uglyfib.go` interpreted
- `testdir:atomicload.go` interpreted
- `testdir:chan/nonblock.go` interpreted
- `testdir:chan/select3.go` interpreted
- `testdir:copy.go` interpreted
- `testdir:divmod.go` interpreted
- `testdir:fixedbugs/issue13169.go` interpreted
- `testdir:fixedbugs/issue16249.go` interpreted
- `testdir:fixedbugs/issue20780b.go` interpreted
- `testdir:fixedbugs/issue22781.go` interpreted
- `testdir:fixedbugs/issue5493.go` interpreted
- `testdir:fixedbugs/issue5963.go` interpreted
- `testdir:fixedbugs/issue59680.go` interpreted
- `testdir:fixedbugs/issue67255.go` interpreted
- `testdir:fixedbugs/issue78081.go` interpreted
- `testdir:fixedbugs/issue79186.go` interpreted
- `testdir:fixedbugs/issue80188.go` interpreted
- `testdir:fixedbugs/issue80196.go` interpreted
- `testdir:fixedbugs/issue9604b.go` interpreted
- `testdir:gcgort.go` interpreted
- `testdir:heapsampling.go` interpreted
- `testdir:ken/chan.go` interpreted
- `testdir:ken/divconst.go` interpreted
- `testdir:ken/modconst.go` interpreted
- `testdir:rangegen.go` compiled and interpreted
- `testdir:stack.go` interpreted
- `testdir:typeparam/issue47272.go` interpreted
- `testdir:typeparam/issue50419.go` interpreted
- `testdir:typeparam/issue50481b.go` interpreted

The pure recursive-int plan plausibly applies to named recursive scalar roots;
the deferred scalar comparison request applies to roots with tight native-value polling.
No credit is claimed for the remaining modes until the Linux replay reports
them by exact ID.

## Worker lane 2026-09-25: measured corpus and shared costs

Local Darwin/arm64, `GOMAXPROCS=2`, built `bashsharp --source=go <root>`
(runoutput roots: generator, then its output), stdout to a pipe, one root at
a time, 60-second local cap. Paired before/after runs are sequential on the
same host (`8b4eafc1` vs the two commits below).

| root | before (s) | after (s) |
| --- | --- | --- |
| atomicload.go | 10.79 | 9.82 |
| chan/select3.go | 0.92 | 0.94 |
| fixedbugs/issue16249.go | 44.29 | 38.98 |
| fixedbugs/issue22781.go | 11.25 | 9.62 |
| fixedbugs/issue67255.go | 11.84 | 4.99 |
| fixedbugs/issue80196.go | 8.97 | 8.75 |
| fixedbugs/issue9604b.go (gen+run) | 8.14 | 7.74 |
| gcgort.go | 7.86 | 6.24 |
| typeparam/issue47272.go | 0.97 | 0.95 |
| typeparam/issue50419.go | 0.86 | 0.80 |
| abi/fibish.go | 8.10 | 7.60 |
| fixedbugs/issue80188.go | > 60 | 56.94 |
| stack.go | > 60 | 58.64 |
| rangegen.go (gen+run) | > 60 (gen) | 31.73 (15.0 + 16.7) |
| heapsampling.go | 37.16 (fails) | 16.02 (fails) |

heapsampling fails identically before and after (`want objects in
[45000: 55000], got [0 0 0]`): the program's allocations are interpreter
cells, invisible to the dependency helper's runtime.MemProfile. That is a
semantic gap, not speed. The unpaired "> 60" before values come from one
sequential pass with the 60-second cap (heapsampling's 37.16 is a separate
150-second base run).

Every other listed root exceeds the 60-second local cap both before and
after: 64bit (generator), abi/fibish_closure, abi/uglyfib, chan/nonblock,
copy, divmod (also > 150 s), issue13169, issue20780b, issue5493, issue5963,
issue59680, issue78081, issue79186, ken/chan, ken/divconst, ken/modconst.

Shared costs found by CPU profiles of 27 roots (20 s each):

- Host collector churn: at GOGC=100 the evaluator's small live heap was
  collected ~180 times per second; stop/start-the-world, preemption and
  span re-commit were about 45% of samples on Darwin. Fixed generally by
  pacing the host collector during Go-source runs (`00256ac5`). Darwin
  overstates this (kevent in startTheWorld, madvise in sysUsed), so the
  Linux gain is expected to be smaller.
- Builtin argument spelling allocated a fresh printer per evaluation
  (`3806ee89`).
- Dependency-bridge round trips dominate ken/divconst, ken/modconst
  (math/rand per iteration, ~2.4 M calls), ken/chan, chan/nonblock, stack
  and the channel/goroutine roots: every native channel operation and
  imported call is one JSON request/response over the helper socket. The
  helper's own profile is almost entirely write/read syscalls and scheduler
  wakeups (dispatch itself is negligible); a round trip costs ~40 us here.
  At that rate these roots cannot meet the bound by evaluator speed-ups; they
  need fewer round trips (for example interpreter-side channels for
  interpreter-only element types), which is an architecture change.
- Pure-evaluation roots (divmod, copy, abi/fibish_closure, abi/uglyfib,
  64bit, issue13169, issue20780b) are 10x+ over the local 8-second target;
  their time is spread over boxed cell copies (`bashPPCopyAssignmentCell`,
  26% of bytes in copy.go), call frames and generic statement dispatch, not
  a single hot path.
- `go` statements snapshot the runner and duplicate the cwd and pipe
  descriptors per goroutine (`dupRunnerDir`/`dupPipeFd`, ~25% of stack.go on
  Darwin; likely cheaper on Linux).
