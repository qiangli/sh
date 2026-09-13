# Sprint 162 per-call profile record

This is the measurement record for Sprint 162 story #93, track B. It is a
design spike, not a product change. The interpreter deadline remains 60 seconds.

## Inputs and identity

- Shell-runtime source: `cc6fdd8b0e3b` before this note.
- Toolchain: Go 1.27.0, darwin/arm64, `GOMAXPROCS=2`.
- Downstream runner: `bashy, GNU Bash 5.3 compatible, version
  5.3.0(1)-bashy`, built with the downstream module unchanged except that its
  `mvdan.cc/sh/v3` replacement named this workspace.
- Selected upstream root: `testdir:abi/fibish.go`. At 510 bytes it is the
  smallest source among the 19 deadline roots. It is read from the authenticated
  Go 1.27 corpus and is never copied or edited here.
- Outside-corpus control: `interp/testdata/sprint162/percall/main.go`, SHA-256
  `98c14d6e7fc8038ac6d2dff352d682e966135a0966008d9cdbd294acbb1b8ee4`.
  It implements ordinary integer `fib`, accepts only `n=25..30`, and defaults
  to 25.

The CLI timing interface was:

```sh
GOMAXPROCS=2 /usr/bin/time -lp ./bashy.real \
  --bashpp --source=go --go-file interp/testdata/sprint162/percall/main.go -- N
```

The in-process profile used the existing `BenchmarkSprint153File` harness,
which parses Go source with `RunMain`, creates a `LangBashPP` runner, and runs
the resulting syntax tree. Representative commands were:

```sh
SPRINT153_BENCH_FILE="$PWD/interp/testdata/sprint162/percall/main.go" \
  GOMAXPROCS=2 go test -run '^$' -bench '^BenchmarkSprint153File$' \
  -benchmem -benchtime=1x -count=3 ./interp

SPRINT153_BENCH_FILE="$PWD/interp/testdata/sprint162/percall/main.go" \
  GOMAXPROCS=2 go test -run '^$' -bench '^BenchmarkSprint153File$' \
  -benchtime=10x -cpuprofile cpu.pprof ./interp
```

The exact corpus root was also run for ten seconds through the same in-process
parse-and-run path under a context deadline while CPU and allocation profiles
were collected. It did not finish, as expected. That temporary profiling test
was removed after the profiles were read; no corpus-derived source is committed.

## Darwin timing and per-call calibration

For this definition, the number of calls made by `fib(n)` is
`2*F(n+1)-1`.

| n | calls | real seconds | user seconds | system seconds | result |
|---:|---:|---:|---:|---:|---|
| 25 | 242,785 | 1.96 | 1.38 | 0.53 | 75,025 |
| 26 | 392,835 | 2.05 | 1.81 | 0.55 | 121,393 |
| 27 | 635,621 | 2.51 | 2.48 | 0.45 | 196,418 |
| 28 | 1,028,457 | 3.41 | 3.65 | 0.44 | 317,811 |
| 29 | 1,664,079 | 5.15 | 5.57 | 0.62 | 514,229 |
| 30 | 2,692,537 | 7.58 | 8.83 | 0.55 | 832,040 |

Least-squares fit of real time against call count:

```text
real seconds = 1.144625 + calls * 2.3725 microseconds
```

That projects the control at `n=40` (331,160,281 calls) to 786.8 seconds,
or 13.11 times the 60-second bound on this Darwin machine. This projection is
for the one-result control. The selected upstream root has two results and
short declarations and was previously calibrated at roughly 22 times the
bound; its exact ten-second profile below shows why it is more expensive.

The `n=25` allocation benchmark was stable across three one-iteration runs:

| ns/op | bytes/op | allocs/op |
|---:|---:|---:|
| 1,819,260,125 | 660,213,632 | 11,784,242 |
| 1,876,695,500 | 659,418,928 | 11,783,508 |
| 2,052,370,500 | 659,414,552 | 11,783,493 |

Using the median allocation row and 242,785 calls gives about **2,716 bytes
and 48.54 allocations per interpreted call**, including an amortized share of
parse/import/startup. The six-size regression removes the fixed startup from
time and gives **2,373 ns per hot call**.

## Allocation attribution

The outside-corpus allocation profile covered three `n=25` operations:
1,928.14 MiB total. The following are flat allocation sites, so their byte
shares do not double count.

| site or group | flat MiB | share | approximate bytes/call | meaning |
|---|---:|---:|---:|---|
| `bashPPCopyAssignmentCell` | 630.13 | 32.68% | 907 | argument and result cells are repeatedly deep-copied |
| `bashPPInvoke` | 187.03 | 9.70% | 269 | parameter map entries, result-name/type slices, and result-cell slices |
| `bashPPReturnScalarExpr` | 168.53 | 8.74% | 243 | scalar return representation |
| `goSourceValueCells` | 162.53 | 8.43% | 234 | typed argument/value boxing |
| `newBashPPScope` | 142.01 | 7.36% | 204 | a fresh typed-locals map per call |
| `bashPPEnterFrame` | 120.52 | 6.25% | 174 | saved caller-state frame object |
| `stmtSync` | 117.50 | 6.09% | 169 | generic statement boundary and its deferred closures |
| named integer type + `math/big` backing | 168.00 | 8.71% | 242 | `go/constant` scalar boxing |
| `bashppParams` | 53.00 | 2.75% | 76 | rebuilding parameter descriptors |
| `newFuncScopeEnviron` | 44.50 | 2.31% | 64 | fresh shell-function environment object |
| all remaining sites | 134.92 | 7.00% | 194 | statement/result slices, import startup, and sampling remainder |

The frame/environment bucket is not just the three constructor rows.
`pprof -list` attributes another 153.03 MiB to inserting the parameter cell
into the fresh `bashPPScope.entries` map. Including that entry plus scope-push
bookkeeping gives approximately **675 bytes/call (25%)** for frame and
environment construction. Scalar cell copies, typed value conversion, and
integer boxing account for approximately **1.5--1.7 KiB/call (55--63%)**,
depending on whether the result/name slices are classified as frame or value
machinery.

The ten-second exact-root allocation profile is consistent but exposes the
two-result/short-declaration surcharge: 10.69 GiB total allocation, of which
cell copies were 33.55%, `goSourceValueCells` 13.36%, short-declaration
snapshotting 12.30%, `bashPPScope.declare` 8.68%, frame entry 3.33%, and the
new typed-scope map 3.29%.

## CPU attribution

The long outside-corpus CPU profile contained 7.99 seconds of samples.
Percentages below are views of the same samples and therefore are not added
together. Multiplying a share by the 2,373 ns hot-call slope gives a useful
wall-equivalent scale, not an independent stopwatch measurement.

| path | sampled CPU | wall-equivalent per call | interpretation |
|---|---:|---:|---|
| generic `cmd`/`stmtSync`/`stmt` call chain, inclusive | 42.05% | about 998 ns | syntax-tree walking and the generic statement boundary surround every recursive call |
| allocation/GC coordination (`kevent`, preemption, sleep, `madvise`), flat lower bound | 43.56% | about 1,034 ns | the 48.5 allocations/call force frequent tiny-heap GC cycles |
| typed call-argument conversion, inclusive | 9.26% | about 220 ns | converting already-typed scalar arguments through the general call interface |
| scalar-cell copying, inclusive | 6.51% | about 154 ns | value ownership is implemented through general cell copies |
| string-map access/assignment and lookup helpers, flat lower bound | about 4% | about 95 ns | locals and environment names remain hash lookups |
| defer/recover-related samples | 2.13% | at most about 51 ns | frame restoration uses `defer`; the program itself has no Go `defer` |
| channel/goroutine setup samples | 0.25% | at most about 6 ns | only import/runtime startup; there is no per-call task or channel on this scalar path |

For the exact upstream root, runtime/GC coordination is even more dominant:
`kevent` alone is 45.52% of the ten-second CPU profile, followed by runtime
preemption at 13.75%, stack growth at 8.15%, and sleep at 6.62%. The generic
interpreter call chain is 16.09% inclusive. This is compatible with the
allocation profile: the program is spending most sampled CPU coordinating
allocation and GC caused by generic value/frame work, rather than computing
integer addition.

## What the 14.5 KiB recursive level contains

`go tool objdump` on the profiling test binary reconfirmed the static frame
sizes on darwin/arm64. The recurring live path is about 14.5 KiB per
interpreted recursive level; it is Go call stack, not heap allocation.

| live frame | reserved bytes | share of 14.5 KiB |
|---|---:|---:|
| `Runner.cmd` | 5,440 | 36.6% |
| `Runner.stmtSync` | 2,992 | 20.2% |
| `Runner.bashPPReturnStmt` | 1,328 | 8.9% |
| `Runner.bashPPInvoke` | 1,136 | 7.7% |
| `Runner.bashPPReturnScalarExpr` | 1,008 | 6.8% |
| `Runner.bashPPEvalScalarExpr` | 896 | 6.0% |
| `Runner.stmt` | 336 | 2.3% |
| `Runner.bashPPScalarFuncCall` | 304 | 2.0% |
| `Runner.stmts` | 192 | 1.3% |
| other live evaluator helpers | about 1,216 | 8.2% |
| **total** | **about 14,848** | **100%** |

Pooling heap frames does not change this table. Only eliminating recursive Go
tree-walker calls in favor of an explicit interpreter frame stack removes the
stack-exhaustion mechanism affecting the Peano/closure roots.

## Non-product scalar-stack bound

A temporary Go benchmark modeled the control as a tiny non-recursive scalar
instruction loop with a fixed `[64]frame` stack. It intentionally omitted
dynamic types, diagnostics, defers, closures, and generic fallback; it was
removed after measurement. Five one-second runs measured 1.746--1.773 ns per
logical fib call and zero allocations.

This is a lower bound on mechanism cost, not a forecast for the product. Before
leaf calibration, 50 times the measured median is about 88 ns/call, or a
27-fold improvement over the current 2,373 ns/call. The leaf result below
shows that this is still too slow on the target. A real fast path has to
demonstrate the leaf-calibrated budget after restoring all observable
semantics; the spike alone authorizes no code.

## Linux leaf calibration

The single valid base-candidate timing ran under the coordinator lock from
`/srv/sprint162/spike-percall`, with the pinned Go 1.27 SDK in `GOROOT` and
`PATH`, `GOMAXPROCS=2`, and the unchanged base runner:

```text
fib(30)=832040
elapsed=30.62 s user=37.64 s system=1.12 s cpu=126%
maximum_rss=122240 KiB exit_status=0
```

The matching Darwin CLI run was 7.58 seconds, so the observed Darwin-to-leaf
wall-time ratio is **4.0396x**. Applying that ratio to the six-size regression
projects the outside control at `n=40` to about **3,178 seconds, or 52.97 times
the 60-second leaf bound**. The ratio also projects fixed startup to about
4.62 seconds, leaving a target of approximately 167 ns/leaf-call. Expressed
in Darwin prototype units, the complete scalar path must stay below about
41.4 ns/call, only **23.5 times** the toy loop's median.

At 20 times toy overhead, the calibrated projection is 51.7 seconds; at 25
times it is 63.5 seconds; at 50 times it is 122.3 seconds. This narrow budget
is the decisive result: the toy proves architectural headroom exists, but does
not make a semantics-complete implementation likely to clear D2 inside this
repair round.
