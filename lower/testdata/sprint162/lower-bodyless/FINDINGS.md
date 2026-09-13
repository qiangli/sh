# Sprint 162 — lane `lower-bodyless` (S162.4 wave 1a, story #94)

Seam: `lower/**`. Authority: the exact upstream Go 1.27 harness. Every row
below is a Barrier B compiled row whose first line was
`LOWER-EUNSUPPORTED: function declaration without body`
(`barrier-b/active-retained-manifest.tsv` 39, `active-151-manifest.tsv` 2).

## Mechanism

A body-less function declaration is a compile-time fact of Go source: the
body is supplied by assembly, a `//go:linkname` or a `//go:wasmimport`, and
gc decides whether one is present. Upstream's `compile` / `errorcheck` /
`compiledir` actions invoke `go tool compile` without `-complete`, which
accepts the declaration; the generic form is a checker error (`generic
function is missing function body`) before lowering, and the Go-source
front end (go/types) already reports it. The emitter now passes a body-less
declaration through as written — receiver, name, type parameters, signature,
and the `//go:` directives the converter attached — instead of refusing it.
The runtime (execution) path still refuses, since it wraps a body it does
not have, and a Bash++ script cannot spell one (the parser demands the
body).

Reproducer: `lower/testdata/sprint162/bodyless/` — `asm-backed.go`
(body-less func + a caller), `method.go` (value and pointer receivers),
`directives.go` (`//go:noescape`, `//go:nosplit`, `//go:linkname`),
`generic.go` (negative: still diagnosed), `with-body.go` (fidelity control,
unchanged). Test: `lower/bodyless_test.go` — golden bytes, D1 identity,
textual + structural survival, the three negatives.

Also fixed on the way (same mechanism, runtime path): `inferChannelParameters`
and `functionResultPlan` walked a nil body and crashed with a nil-pointer
panic before the emitter could diagnose the declaration; both now skip a
body-less owner.

## Rows

Status legend: `emitter` = the emitter half is done (this lane); the row
still needs the backend lane's compile-only invocation (S162.4b, D3(a)) to
avoid cmd/go's implied `-complete`, which makes gc report `missing function
body` on the generated module exactly as it would on the input.

| root | recipe | first cause | mechanism | status |
|---|---|---|---|---|
| testdir:abi/result_live.go | errorcheck -0 -live | body-less func | pass-through | emitter |
| testdir:escape2.go | errorcheck -0 -m -l | body-less func | pass-through | emitter |
| testdir:escape2n.go | errorcheck -0 -N -m -l | body-less func | pass-through | emitter |
| testdir:escape_calls.go | errorcheck -0 -m -l | body-less func | pass-through | emitter |
| testdir:fixedbugs/bug089.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/bug150.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/bug245.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/bug392.go | compiledir | body-less func (pkg2.go) | pass-through | emitter |
| testdir:fixedbugs/bug443.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue11699.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue13587.go | errorcheck -0 -l -d=wb | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue17596.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue19168.go | errorcheck -0 -l -d=wb | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue20780.go | errorcheck | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue23311.go | compiledir | body-less linkname func (main.go) | pass-through | emitter |
| testdir:fixedbugs/issue28430.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue30061.go | compile | body-less linkname func | pass-through | emitter |
| testdir:fixedbugs/issue31010.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue31915.go | compile -d=ssa/check/on | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue4099.go | errorcheck -0 -m | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue42944.go | errorcheck -0 -live | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue43479.go | compiledir | body-less func (a.go) | pass-through | emitter |
| testdir:fixedbugs/issue43677.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue45323.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue45344.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue48097.go | errorcheck -complete | body-less func | pass-through | emitter; recipe itself is -complete (expects gc's missing function body) |
| testdir:fixedbugs/issue53454.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue58826.go | compile -dynlink | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue74836.go | compile | body-less func | pass-through | emitter |
| testdir:fixedbugs/issue9608.go | rundir | body-less func (issue9608.go) | pass-through | emitter; D3(b) executing root |
| testdir:func2.go | compile | body-less func | pass-through | emitter |
| testdir:import.go | compile | body-less func | pass-through | emitter |
| testdir:import2.go | compiledir | body-less func (import2.go) | pass-through | emitter |
| testdir:internal/runtime/sys/inlinegcpc.go | errorcheck -0 -+ -p=internal/runtime/sys -m | body-less func | pass-through | emitter |
| testdir:linkname.go | errorcheckandrundir -0 -m -l=4 | body-less linkname func (linkname2.go) | pass-through | emitter; D3(b) executing root |
| testdir:live1.go | compile | body-less func | pass-through | emitter |
| testdir:live2.go | errorcheck -0 -live -wb=0 | body-less func | pass-through | emitter |
| testdir:live_uintptrkeepalive.go | errorcheck -0 -m -live -std | body-less func | pass-through | emitter |
| testdir:nilcheck.go | errorcheck -0 -N -d=nil | body-less func | pass-through | emitter |
| testdir:nilptr3.go | errorcheck -0 -d=nil | body-less func | pass-through | emitter |
| testdir:parentype.go | compile | body-less func | pass-through | emitter |

In-process check (darwin, `gosource.Load` + `lower.Compile` on each root's
named file): 40/41 lower without a diagnostic; `bug392.dir/pkg2.go` imports
its sibling `./one`, which needs the package map the backend passes (not a
lowering matter).

## Requests to other seams

* backend lane (`bashpp-tests/tools/upstream-harness/testdata/backend/`,
  S162.4b): compile-only recipes (`compile`, `errorcheck`, `compiledir`)
  must invoke the pinned toolchain the way upstream's `compile` action does
  — `go tool compile` without `-complete` — on the generated module.
  cmd/go's `go build` adds `-complete` to any package with no non-Go files,
  and gc then reports `<file>:<line>:<col>: missing function body` for
  every body-less declaration (linknamed ones exempt), on the generated
  module exactly as on the input. Until that lands every row above stays
  FAIL with that new first line, which is this lane's evidence that the
  emitter half is done.

## Not this lane (recorded, not touched)

* Wave 2 fidelity (S162.4 wave 2): an untyped constant argument to a
  parameter of a sized type is retyped by the emitter
  (`Store(&x, 1)` → `Store(&x, uint64(1))`, independent of whether the
  callee has a body). Not in the Sprint 152 `untyped-constants` fixture's
  shape; a `-m` fidelity row candidate.
