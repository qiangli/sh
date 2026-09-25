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
