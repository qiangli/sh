# Sprint 162 — the `cmd/compile/internal/abt` spike (S162.2 step 1 evidence)

Story #92 (`3cccf8001037`). Design lane `packages-design`; no product code.
The script is `docs/evidence/sprint162-abt-spike.sh`; the design note that
reads these numbers is `docs/bashpp-multi-package-execution.md` §6.

## Venue and identities

- The leaf host (2 vCPU, `Linux x86_64`), one coordinator: the whole run
  under `flock` on the host's coordinator lock, queued behind run 0
  (run 0 ended 02:41:54Z; the spike ran **02:58:38Z – 03:01:44Z**, wall
  3 min 6 s including a cold std build).
- `$SDK` = the pinned, authenticated Go 1.27.0 linux/amd64
  (`go` sha256 `1db869c560a19357…`), mirrored by symlink into `$OUT/goroot`
  (the package gate's shape) and used as `GOROOT`; `GOTOOLCHAIN=local`,
  `GOMAXPROCS=2`, `GOFLAGS=-p=2`, fresh `GOCACHE=$OUT/gocache`,
  `TMPDIR=$OUT/tmp`, `POSIXLY_CORRECT` unset.
- Base candidate `$BASE/bashy/bin/bashy.real` = `5.3.0(1)-bashy-dev
  (548c3a4)`, sha256 `9f18849d5f0d6c10…` (the Barrier B binary); shell
  runtime source `$BASE/sh` at `e7cd317e`.
- Package: `cmd/compile/internal/abt` = `avlint32.go` + `avlint32_test.go`,
  digest (matrix form) `e7355bbdc79a2339…` = `package-matrix.tsv`'s row.
- `$OUT` = the spike's fresh output directory on the leaf host (logs, raw
  outputs, generated files; never `/tmp`).

## Results — the numbers

| step | what | result | wall (2 vCPU) |
|---|---|---|---|
| N cold | native `go test -count=1 -json cmd/compile/internal/abt`, fresh GOCACHE | PASS, 9 tests | 19.63 s (std build) |
| N warm | same | PASS, 9 tests | **0.39 s** |
| N testmain | `go test -c -work` → `$WORK/b001/_testmain.go` (cmd/go's own enumeration: 9 tests, `_test "cmd/compile/internal/abt"`, `testing/internal/testdeps`) | captured | 0.50 s |
| R0 interpreted | the backend's exact argv on the base candidate (`--go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=<2 files> --go-file _testmain.go -- -test.v`) | **exit 2**: `_testmain.go:10:2: could not import testing/internal/testdeps (use of internal package testing/internal/testdeps not allowed)` — the Barrier B first line, reproduced | 0.48 s |
| R0 compiled | the backend's exact `transpile` argv | **exit 2**, same line | 0.40 s |
| C1 transpile | `transpile --go-import-path cmd/compile/internal/abt --go-file avlint32.go --go-file avlint32_test.go` (the package as a library) | exit 0; 3,292 lines; **0 runtime imports, 0 mangled names**; hand-split by `//line` origin into `avlint32.go` (1,130 lines, 58 top-level decls = original 58) and `avlint32_test.go` (1,066 lines, 34 decls vs original 33 — one `if x := …` hoist) | 0.59 s |
| **C1 go test -overlay** | `go test -count=1 -json -overlay=$OUT/C1/overlay.json cmd/compile/internal/abt` — the overlay replaces both original files with the generated ones at the original import path; cmd/go builds its own `_testmain.go` | **PASS, 9/9 tests** (package terminal `pass`) | **0.68 s** (cold for the package), 0.34 s warm |
| C1 proof `-x` | compile argv for `-p cmd/compile/internal/abt` (fresh GOCACHE) | positional inputs = `$OUT/C1/gen/avlint32.go $OUT/C1/gen/avlint32_test.go` — **2 generated, 0 original**; `-trimpath "<generated>=><original>"` maps positions back | 18.35 s (std rebuild under `-x`) |
| C1 canary | a `TestBashppOverlayCanary` present only in the generated test file, `-run` it under the overlay | ran and passed (`avlint32_test.go:1068: overlay-active`) | 0.72 s |
| C1 negative | the same `-run` without the overlay | `no tests to run` (canary absent natively) | 0.39 s |
| C2 transpile | driver derived from `_testmain.go` (same 9-entry tests table, `testing.Main` instead of `MainStart(testdeps.TestDeps{},…)`) + the package map → flat `main.go` | exit 0; 3,309 lines; mapped package flattened with `__gosource_*` names; imports fmt/strconv/strings/testing/regexp | 1.17 s |
| C2 go build | `go build -C $OUT/C2/bashpp -o program .` (module with `replace mvdan.cc/sh/v3 => $BASE/sh`) | exit 0 | 0.62 s |
| **C2 run** | `program -test.v` | **PASS 9/9** | 0.01 s |
| C3 go build (negative) | the flat program with the real `_testmain.go` body (`testing/internal/testdeps`) through `go build` | **refused**: `main.go:11:8: use of internal package testing/internal/testdeps not allowed` (cmd/go's rule, as the note predicts) | 0.04 s |
| C3 importcfg | `go list -export -deps -f 'packagefile {{.ImportPath}}={{.Export}}' fmt os strconv strings testing testing/internal/testdeps` in the module dir | exit 0; **129 `packagefile` lines, `testing/internal/testdeps` included** (command-line targets are exempt from the internal rule) | 0.16 s |
| C3 compile/link/run | `go tool compile -p main -importcfg` → `go tool link -importcfg` → run | first pass: **script defect** (the hand-assembly cut the mapped package's test functions, `undefined: __gosource_pkg_0_TestApplicInsert` — the generated file lists the program's declarations before the mapped package's); corrected assembly proven end to end on the darwin dev box with Go 1.26 (compile, link, 9/9 PASS); the corrected leaf re-run is queued under the lock — see "C3 re-run" below | — |
| I1 interpreted (60 s) | the C2 driver + map through the interpreter (`bashy.real --bashpp --source=go … --go-file driver.go -- -test.v`): the check passes (no `internal` import outside the map), the native bridge starts `testing`/`regexp`, `testing.Main` calls back into the interpreted `TestApplicInsert` | **exit 2 after 7.72 s**: `avlint32.go:104:3: BASHPP-EASSIGN-TYPE: untyped result is not assignable to *__gosource_pkg_0_node32` — an evaluator row (S162.1) on the mapped package's mangled pointer type, before any timing question | 7.72 s (bridge build + first test) |
| I1 unbounded / per-test | same, no bound; then `-test.run ^TestBounds$`, `^TestEquals$`, `^TestApplicInsert$` | same first line every time; `--- FAIL` after the evaluator error; 0 tests pass | 3.9–4.1 s each |
| S scale | `transpile` of every package root as a library (GoFiles + TestGoFiles from `go list` on the real SDK tree; XTestGoFiles excluded), 600 s bound each | **17/26 exit 0**; 9 exit 2 with a first line each (table below); the largest (`ssa`, 463,909 LOC) fails in 13.9 s; `cmd/compile` (492 LOC, `package main`) takes 65.5 s — the module importer's first `go list -export -deps` of the compiler's closure | 0.2 – 65.5 s |

`survivors under $OUT: 3` at the end are the script's own `ps | grep`
matches (the pipeline itself); no test, compiler or Bash++ process survived.

### S — library transpile of all 26 (the compiled route's first gate)

`package · go/test/xtest/.s files (as `go list` selects them on linux/amd64) · LOC · exit · wall · first line`

- `cmd/compile` · 2/1/0/0 · 492 · exit 0 · 65.5 s · —
- `cmd/compile/internal/abt` · 1/1/0/0 · 1525 · exit 0 · 0.6 s · —
- `cmd/compile/internal/amd64` · 4/0/2/0 · 7083 · exit 2 · 0.6 s · cmd/compile/internal/amd64/ssa.go:1742:3: LOWER-ETYPE: cannot convert x (variable of type *big.Int) to type int64
- `cmd/compile/internal/base` · 10/1/0/0 · 2203 · exit 0 · 1.4 s · —
- `cmd/compile/internal/compare` · 1/1/0/0 · 486 · exit 0 · 0.7 s · —
- `cmd/compile/internal/devirtualize` · 2/1/0/0 · 1663 · exit 0 · 1.0 s · —
- `cmd/compile/internal/dwarfgen` · 4/2/0/0 · 2000 · exit 0 · 3.5 s · —
- `cmd/compile/internal/importer` · 3/1/0/0 · 1562 · exit 0 · 1.1 s · —
- `cmd/compile/internal/inline/inlheur` · 21/5/0/0 · 5103 · exit 0 · 1.3 s · —
- `cmd/compile/internal/ir` · 27/4/0/0 · 10081 · exit 2 · 1.2 s · cmd/compile/internal/ir/abi.go:7:1: LOWER-ETYPE: "unsafe" imported and not used
- `cmd/compile/internal/liveness` · 5/1/0/0 · 4005 · exit 0 · 1.4 s · —
- `cmd/compile/internal/logopt` · 1/1/0/0 · 790 · exit 0 · 0.6 s · —
- `cmd/compile/internal/loopvar` · 1/0/1/0 · 618 · exit 0 · 0.7 s · —
- `cmd/compile/internal/noder` · 17/1/0/0 · 11303 · exit 2 · 2.6 s · cmd/compile/internal/noder/writer.go:2479:10: gosource: unsupported type switch initializer
- `cmd/compile/internal/rangefunc` · 1/0/1/0 · 1541 · exit 0 · 0.6 s · —
- `cmd/compile/internal/reflectdata` · 4/0/1/0 · 2951 · exit 0 · 1.9 s · —
- `cmd/compile/internal/ssa` · 96/32/4/1 · 463909 · exit 2 · 13.9 s · cmd/compile/internal/ssa/allocators.go:38:12: gosource: unsupported expression *ast.ArrayType
- `cmd/compile/internal/ssagen` · 10/1/0/0 · 16882 · exit 2 · 2.6 s · cmd/compile/internal/ssagen/nowb.go:115:22: gosource: unsupported expression *ast.ArrayType
- `cmd/compile/internal/syntax` · 16/8/0/0 · 9664 · exit 0 · 1.4 s · —
- `cmd/compile/internal/test` · 2/43/0/0 · 30874 · exit 0 · 3.6 s · —
- `cmd/compile/internal/typecheck` · 16/1/0/0 · 6310 · exit 0 · 1.0 s · —
- `cmd/compile/internal/types` · 12/2/1/0 · 4393 · exit 2 · 0.7 s · cmd/compile/internal/types/alg.go:7:1: LOWER-ETYPE: "unsafe" imported and not used
- `cmd/compile/internal/types2` · 69/8/20/0 · 24021 · exit 2 · 1.0 s · cmd/compile/internal/types2/interface.go:124:24: gosource: unsupported expression *ast.ArrayType
- `cmd/internal/testdir` · 0/0/1/0 · 0 · exit - · - s · no GoFiles/TestGoFiles (xtest only)
- `go/types` · 75/9/26/0 · 25462 · exit 2 · 1.5 s · go/types/interface.go:163:24: gosource: unsupported expression *ast.ArrayType
- `internal/types/errors` · 2/0/1/0 · 1684 · exit 0 · 0.2 s · —

Reading: 17 packages lower as a library on the base candidate with no
emitter change (the same shape C1 ran `go test` on). The 9 that do not are
product rows of three kinds the overlay route would surface honestly —
`gosource: unsupported expression *ast.ArrayType` (`ssa`, `ssagen`,
`types2`, `go/types`; S162.1's compiled `gosource: unsupported` class),
`gosource: unsupported type switch initializer` (`noder`), `LOWER-ETYPE:
"unsafe" imported and not used` (`ir`, `types`; the hoisted-import pruning
a per-file emitter removes), `LOWER-ETYPE: cannot convert x (variable of
type *big.Int) to type int64` (`amd64`). `cmd/internal/testdir` has only an
external test package (0 GoFiles/TestGoFiles), so its unit is the xtest
import-edge case of the note's §5.1. A library transpile is not a `go test`
verdict: it is the gate before the overlay.

## Commands (as run, `$OUT`/`$SDK`/`$BASE` substituted)

```text
### N-native-go-test-cold
$ $OUT/goroot/bin/go test -count=1 -json cmd/compile/internal/abt
exit=0 wall=19.63s (bound 600s) stdout=40 lines stderr=0 lines
### N-native-go-test
$ $OUT/goroot/bin/go test -count=1 -json cmd/compile/internal/abt
exit=0 wall=0.39s (bound 600s) stdout=40 lines stderr=0 lines
### N-testmain-work
$ $OUT/goroot/bin/go test -c -work -o $OUT/N/abt.test cmd/compile/internal/abt
exit=0 wall=0.50s (bound 600s) stdout=0 lines stderr=1 lines
### R0-interpreted
$ $BASE/bashy/bin/bashy.real --bashpp --source=go --go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go,$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go --go-file $OUT/N/_testmain.go -- -test.v
exit=2 wall=0.48s (bound 60s) stdout=0 lines stderr=1 lines
### R0-compiled-transpile
$ $BASE/bashy/bin/bashy.real transpile --bashpp --source=go --go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go,$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go --go-file $OUT/N/_testmain.go -o $OUT/R0/bashpp/main.go --map $OUT/R0/bashpp/main.go.map
exit=2 wall=0.40s (bound 60s) stdout=0 lines stderr=1 lines
### C1-transpile-library
$ $BASE/bashy/bin/bashy.real transpile --bashpp --source=go --go-import-path cmd/compile/internal/abt --go-file $OUT/goroot/src/cmd/compile/internal/abt/avlint32.go --go-file $OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go -o $OUT/C1/gen/flat.go --map $OUT/C1/gen/flat.go.map
exit=0 wall=0.59s (bound 60s) stdout=0 lines stderr=0 lines
### C1-go-test-overlay
$ $OUT/goroot/bin/go test -count=1 -json -overlay=$OUT/C1/overlay.json cmd/compile/internal/abt
exit=0 wall=0.68s (bound 600s) stdout=40 lines stderr=0 lines
### C1-go-test-overlay-warm
$ $OUT/goroot/bin/go test -count=1 -json -overlay=$OUT/C1/overlay.json cmd/compile/internal/abt
exit=0 wall=0.34s (bound 600s) stdout=40 lines stderr=0 lines
### C1-go-test-overlay-x
$ $OUT/goroot/bin/go test -count=1 -x -overlay=$OUT/C1/overlay.json cmd/compile/internal/abt
exit=0 wall=18.35s (bound 600s) stdout=1 lines stderr=7263 lines
### C1-canary
$ $OUT/goroot/bin/go test -count=1 -v -run TestBashppOverlayCanary -overlay=$OUT/C1/overlay2.json cmd/compile/internal/abt
exit=0 wall=0.72s (bound 600s) stdout=5 lines stderr=0 lines
### C1-native-canary-negative
$ $OUT/goroot/bin/go test -count=1 -v -run TestBashppOverlayCanary cmd/compile/internal/abt
exit=0 wall=0.39s (bound 600s) stdout=3 lines stderr=0 lines
### C2-transpile
$ $BASE/bashy/bin/bashy.real transpile --bashpp --source=go --go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go,$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go --go-file $OUT/C2/driver.go -o $OUT/C2/bashpp/main.go --map $OUT/C2/bashpp/main.go.map
exit=0 wall=1.17s (bound 60s) stdout=0 lines stderr=0 lines
### C2-go-build
$ $OUT/goroot/bin/go build -C $OUT/C2/bashpp -o $OUT/C2/program .
exit=0 wall=0.62s (bound 600s) stdout=0 lines stderr=0 lines
### C2-run
$ $OUT/C2/program -test.v
exit=0 wall=0.01s (bound 600s) stdout=19 lines stderr=0 lines
### C3-go-build-negative
$ $OUT/goroot/bin/go build -o $OUT/C3/program-gobuild .
exit=1 wall=0.04s (bound 600s) stdout=0 lines stderr=2 lines
### C3-go-list-importcfg
$ $OUT/goroot/bin/go list -export -deps -f {{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}} fmt os strconv strings testing testing/internal/testdeps
exit=0 wall=0.16s (bound 600s) stdout=129 lines stderr=0 lines
### C3-compile
$ $OUT/goroot/bin/go tool compile -p main -importcfg $OUT/C3/importcfg -o $OUT/C3/main.o main.go
exit=2 wall=0.02s (bound 600s) stdout=11 lines stderr=0 lines
### I1-interpreted-60s
$ $BASE/bashy/bin/bashy.real --bashpp --source=go --go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go,$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go --go-file $OUT/C2/driver.go -- -test.v
exit=2 wall=7.72s (bound 60s) stdout=1 lines stderr=1 lines
### I1-interpreted-unbounded
$ $BASE/bashy/bin/bashy.real --bashpp --source=go --go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go,$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go --go-file $OUT/C2/driver.go -- -test.v
exit=2 wall=4.07s (bound 1800s) stdout=2 lines stderr=16 lines
### I1-interpreted-TestBounds
$ $BASE/bashy/bin/bashy.real --bashpp --source=go --go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go,$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go --go-file $OUT/C2/driver.go -- -test.v -test.run ^TestBounds$
exit=2 wall=3.89s (bound 300s) stdout=1 lines stderr=1 lines
### I1-interpreted-TestEquals
$ $BASE/bashy/bin/bashy.real --bashpp --source=go --go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go,$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go --go-file $OUT/C2/driver.go -- -test.v -test.run ^TestEquals$
exit=2 wall=4.08s (bound 300s) stdout=2 lines stderr=16 lines
### I1-interpreted-TestApplicInsert
$ $BASE/bashy/bin/bashy.real --bashpp --source=go --go-import-path cmd/compile/internal/abt.test --go-package cmd/compile/internal/abt=$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go,$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go --go-file $OUT/C2/driver.go -- -test.v -test.run ^TestApplicInsert$
exit=2 wall=3.88s (bound 300s) stdout=2 lines stderr=16 lines
```

The overlay file `$OUT/C1/overlay.json`:

```json
{"Replace":{"$OUT/goroot/src/cmd/compile/internal/abt/avlint32.go":"$OUT/C1/gen/avlint32.go",
            "$OUT/goroot/src/cmd/compile/internal/abt/avlint32_test.go":"$OUT/C1/gen/avlint32_test.go"}}
```

The hand-split of the generated flat file (the mechanism a per-file emitter
implements): every top-level declaration in the transpile output is
preceded by `//line <origin>:<line>`; declarations were routed to the
generated file named after their origin, `package main` rewritten to
`package abt`, and each part given the imports its identifiers use
(`fmt`/`strconv`/`strings` for `avlint32.go`; `fmt`/`strconv`/`testing` for
the test file). No other edit.

## Darwin pre-checks (dev box, Go 1.26.0 darwin/arm64 — not leaf evidence)

- importcfg route: a `package main` importing both `testing/internal/testdeps`
  and `cmd/compile/internal/abt` — `go build`: `use of internal package
  cmd/compile/internal/abt not allowed`; `go list -export -deps` (121
  packagefile lines) → `go tool compile -p main -importcfg` → `go tool link
  -importcfg` → the binary runs a test to PASS.
- C1 overlay route dry-run: PASS 9/9 in 0.56 s; canary 1/0.
- `cmd/compile/internal/base` as a library: exit 0 under the real GOROOT,
  exit 2 (`could not import internal/buildcfg`) under the symlink-mirrored
  GOROOT — the `isDirInGOROOT` venue sensitivity recorded in the note §2.1.
  The leaf's S step therefore uses the real SDK tree.
- I1: identical first line to the leaf (`avlint32.go:104:3
  BASHPP-EASSIGN-TYPE`).

## C3 re-run (corrected hand-assembly, leaf host)

Queued under the coordinator lock behind other lanes' jobs at the time of
this commit (`$OUT/spike-c3-rerun.log`); the corrected assembly (drop the
driver's two one-line functions, keep the mapped package's declarations that
follow them) is what the committed script now does. The mechanism itself is
already proven twice: on the leaf, the pinned Go 1.27 `go list -export -deps`
emits the 129-line importcfg with `testing/internal/testdeps` from a module
outside the tree; on the dev box, the same compile/link/run of the corrected
file passes 9/9. If the re-run lands before the manager records D1 its lines
are appended here in a follow-up commit.
