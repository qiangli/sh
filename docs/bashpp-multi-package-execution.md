# Bash++ multi-package execution — the 26 `package:` roots (S162.2 step 1, design)

Status: **DESIGN NOTE for D1, Sprint 162** (story #92, `3cccf8001037`).
No product code. Written from the code (`sh/gosource/{source,packages}.go`,
`sh/lower/module_importer.go`, `sh/interp/bashpp_{import,native_bridge,
import_scratch}.go`, the frozen Go 1.27 `cmd/go/internal/{load,test}`) and
from one measured spike on the leaf host
(`docs/evidence/sprint162-abt-spike.md`, script
`docs/evidence/sprint162-abt-spike.sh`). The manager records D1 from §7.

## 0. Summary

- The 26 roots do **not** fail where the story brief says. The interpreted
  refusal `--go-package … require --check or --go-list` was lifted in S151.1
  (`gosource.Load` links every mapped package into the one program the
  interpreter runs; `bashy/internal/cli/gosource.go` "The explicit package map
  is linked at lowering time"). Barrier B's first line for 25 of 26 roots, in
  **both** modes, is `could not import <internal path> (use of internal
  package … not allowed)` — and that message is **Bash++'s own** check
  (`lower/module_importer.go:checkInternalVisibility`), raised before any
  native `go build` runs. The 26th (`cmd/compile/internal/ssa`) is the
  backend's `non-Go inputs` refusal (two `*_test.s` files).
- The first cause is one design defect with three faces: **`internal`
  visibility is decided from directories, never from the package identity
  (`-p`) the map already carries.** (1) the check (`module_importer.go`),
  (2) the runtime import bridge's resolver (`interp/bashpp_import.go`), and
  (3) the native builds of the bridge worker and of the generated module,
  which go through `cmd/go` and therefore inherit `cmd/go`'s own
  directory/module rule. Go's model separates policy (`cmd/go`) from the
  policy-free compiler (`-importcfg`); Bash++ applies `cmd/go`'s policy at
  the wrong identity and then asks `cmd/go` to build from the wrong place.
- **Interpreted (a)**: the mechanism exists (S151.1 flattening + the native
  bridge for the closure). What is missing is identity-keyed visibility with
  `cmd/go`'s own testmain exception (one function, three call sites), an
  `importcfg`-driven worker build, and — the real cost — the interpreter
  executing 1.5k–467k LOC of compiler source under S162.1's evaluator gaps
  and S162.3's per-call cost inside `cmd/go`'s 10-minute package bound. The
  spike's first interpreted failure on the smallest root is an evaluator
  row (`BASHPP-EASSIGN-TYPE`), not a packaging row.
- **Compiled (b)**: the honest route is **`-overlay` at the original import
  path** — Bash++ emits the tested package as a *library* (per original
  file, same package clause, 152 D1 identity lowering) and the pinned
  `go test -overlay` builds and runs the *original* `_testmain.go` over the
  generated files inside the SDK tree. `cmd/go` then decides `internal`
  visibility exactly as it does natively. A generated *module* placed inside
  a GOROOT copy is **impossible by construction** (no directory is inside
  both `testing/` and `cmd/compile/`, and `_testmain.go` needs
  `testing/internal/testdeps`). The flat-program-plus-`importcfg` route works
  and is the fallback (and the bridge worker needs the same build anyway).
- **Numbers** (§6): see the spike record; on 2 vCPU the native `go test` of
  the 26 already ranges from 5 ms to **256 s** (`cmd/internal/testdir`,
  which *is* the corpus runner), so the interpreted mode of the large roots
  is a design limitation to record by ID whatever D1 says.
- **Recommendation (§7)**: D1 = (a′): compiled via the overlay route for all
  26 (mechanisms in `lower` + `gosource` + the backend seam); interpreted via
  the identity-keyed visibility + importcfg worker for the roots whose
  native time leaves headroom under the bound, the rest recorded by ID.

## 1. What a `package:` root is

`bashpp-tests/tools/upstream-harness/package-gate.sh` (S150.8) builds the
frozen Go 1.27 `cmd/go` with a seven-line patch at the one site where the
test binary would run (`cmd/go/internal/test/test.go:1668`) and an overlay
file `testdata/go-backend/bashpp_backend.go`. The unmodified `cmd/go`
enumerates the tests (`load.TestPackagesFor` → `_testmain.go`), builds the
native test binary, and at the patched site `bashppTestPlan` replaces the
argv with the Bash++ form of that binary:

```text
interpreted: bashy --bashpp --source=go --go-import-path <pkg>.test
               --go-package <pkg>=<GoFiles+TestGoFiles>
               [--go-package <pkg>_test=<XTestGoFiles>]
               --go-file $WORK/b001/_testmain.go -- <test flags>
compiled:    bashy transpile … same map … -o $WORK/b001/bashpp/main.go --map …
             go build -C $WORK/b001/bashpp -o program .
             exec program <test flags>
```

The program is the package under test with its in-package test files
(`ptest`), the external test package (`pxtest`, 12 of the 26 have one) and
`cmd/go`'s generated `_testmain.go` as `package main`. `_testmain.go`
imports `os`, `testing`, **`testing/internal/testdeps`** and
`_test "<pkg>"` (`load/test.go:820`, `testmainTmpl`). The bound is `go
test -timeout=10m` per package (`corpus-gate.sh:369`), not the 60 s testdir
deadline; `cmd/go` applies it to whatever argv the patched site returns
(`test.go:1651`, `testKillTimeout`).

The 26, with the native `go test` wall time run 0 measured on the leaf
host (2 vCPU, `leaf-162r0/evidence/evidence-native/packages/`):

| package | Go / test / xtest files | `.s` | LOC | native 2 vCPU |
|---|---:|:-:|---:|---:|
| cmd/compile (package main) | 2 / 1 / 0 | – | 492 | 24.5 s |
| cmd/compile/internal/abt | 1 / 1 / 0 | – | 1,525 | 0.008 s |
| cmd/compile/internal/amd64 | 4 / 3 / 3 | – | 7,617 | 3.0 s |
| cmd/compile/internal/base | 12 / 1 / 0 | – | 2,236 | 0.005 s |
| cmd/compile/internal/compare | 1 / 1 / 0 | – | 486 | 0.005 s |
| cmd/compile/internal/devirtualize | 2 / 1 / 0 | – | 1,663 | 0.005 s |
| cmd/compile/internal/dwarfgen | 4 / 2 / 0 | – | 2,000 | 0.55 s |
| cmd/compile/internal/importer | 3 / 1 / 0 | – | 1,562 | 30.9 s |
| cmd/compile/internal/inline/inlheur | 22 / 5 / 0 | – | 5,143 | 0.56 s |
| cmd/compile/internal/ir | 29 / 4 / 0 | – | 10,485 | 0.007 s |
| cmd/compile/internal/liveness | 5 / 1 / 0 | – | 4,006 | 0.024 s |
| cmd/compile/internal/logopt | 1 / 1 / 0 | – | 790 | 0.27 s |
| cmd/compile/internal/loopvar | 1 / 1 / 1 | – | 1,061 | 46.5 s |
| cmd/compile/internal/noder | 17 / 1 / 0 | – | 11,316 | 0.008 s |
| cmd/compile/internal/rangefunc | 1 / 1 / 1 | – | 3,747 | 0.010 s |
| cmd/compile/internal/reflectdata | 4 / 1 / 1 | – | 3,098 | 0.005 s |
| cmd/compile/internal/ssa | 97 / 36 / 4 | 2 (`*_test.s`) | 467,134 | 129.5 s |
| cmd/compile/internal/ssagen | 10 / 1 / 0 | – | 16,908 | 0.010 s |
| cmd/compile/internal/syntax | 16 / 8 / 0 | – | 9,653 | 4.6 s |
| cmd/compile/internal/test | 2 / 43 / 0 | – | 30,933 | 83.9 s |
| cmd/compile/internal/typecheck | 17 / 1 / 0 | – | 6,564 | 0.34 s |
| cmd/compile/internal/types | 12 / 3 / 1 | – | 4,466 | 0.005 s |
| cmd/compile/internal/types2 | 69 / 28 / 20 | – | 31,751 | 14.8 s |
| cmd/internal/testdir | 0 / 1 / 1 | – | 2,072 | **256.2 s** |
| go/types | 76 / 35 / 26 | – | 35,478 | 18.7 s |
| internal/types/errors | 3 / 1 / 1 | – | 1,998 | 0.35 s |

File counts are the checkout's `*.go` names (build constraints not applied);
LOC is all `*.go` in the directory. `cmd/internal/testdir`'s test *is* the
corpus harness: it compiles and runs the whole `test/` tree.

## 2. Where the 26 fail today — three faces of one defect

### 2.1 Face 1 — the check refuses `internal` from the wrong identity (both modes, 25/26 first lines)

`gosource.Load` (`gosource/source.go:142`) checks every explicit package
and then the program through `mapImporter` (`gosource/packages.go`): the
map first, then `Options.Importer` for everything else. `bashy` supplies
`lower.NewModuleImporter(opts.Dir)` (`bashy/internal/agentos/gosource.go:73`)
with **`Dir` = the directory of the first `--go-file`**
(`cli/gosource.go: readGoSourceFiles`) — for a package root that is
`$WORK/b001`, the scratch directory holding `_testmain.go`.

`moduleImporter.ImportFrom` (`lower/module_importer.go`) runs
`checkInternalVisibility(path, m.callerPath, m.dir, sdk.Root)` before any
`go list`. `m.callerPath` is `determineCallerPath(dir, sdk)`: `go list -m`
in `dir`, else a GOPATH relative path, else `filepath.Base(dir)` — i.e.
**`b001`**. The rule then refuses every `internal` element:

- top-level `internal/...` (`internal/buildcfg`, `internal/testenv`, …)
  needs `isDirInGOROOT(m.dir, sdk.Root)` — `$WORK` is not in GOROOT;
- `cmd/compile/internal/...`, `testing/internal/...` need `callerPath` to
  have the parent-of-internal prefix — `b001` has none.

`go/types` hands `ImportFrom` the importing *file's* directory (`srcDir`)
on every call; `moduleImporter` ignores it. And the identity the map
carries — `gosource.Options.ImportPath` (`--go-import-path`, the `-p` of the
program) and each `PackageSpec.Path` (`mapImporter.from`) — never reaches
the visibility decision. Two consequences measured on darwin during this
step:

- `abt` itself imports nothing internal; its row fails only on
  `_testmain.go:10:2 … testing/internal/testdeps`. `cmd/go` allows that
  import for exactly one importer, the generated testmain
  (`load/pkg.go:1481`, `importerPath == "testmain"`); Bash++ has no
  equivalent.
- The decision is venue-sensitive: with a symlink-mirrored GOROOT (the
  package gate's own shape, `package-gate.sh` "mirror the SDK by symlink")
  `isDirInGOROOT` compares an `EvalSymlinks`'d directory against the
  un-resolved mirror path and refuses every top-level `internal` import of a
  library that is *inside* the tree (`cmd/compile/internal/base` as a
  library: exit 2 under the mirror, exit 0 under the real GOROOT). A rule
  keyed on identity has no such failure mode.

### 2.2 Face 2 — the runtime resolver (interpreted only)

Even with Face 1 fixed, the interpreter's `import` of a native dependency
goes through `nativeBashPPEvaluator.Resolve` (`interp/bashpp_import.go:105`):
`go list -json` in the module dir, then

- `syntax.BashPPStdlibImportAllowed(path)` — the reviewed Go 1.27 inventory
  (`syntax/go127stdlib_generated.go`, 180 paths). Its generator
  (`syntax/gen_go127stdlib.go:125–129`) skips `cmd/...` and every path with
  an `internal`, `vendor` or `testdata` element. `testing/internal/testdeps`,
  `internal/testenv`, `cmd/internal/obj`, … are all `capUnreviewedStdlib` →
  `policyRefuse` (`interp/bashpp_eval.go`).
- `validateBashPPImportVisibility(req.Dir, info.Dir, path)` — again a
  directory rule (`pathWithin(root, importerDir)`), with the same wrong
  importer directory.

The inventory is a reviewed security boundary ("quietly widening it to
anything `go list` calls standard would defeat the review"). Admitting
`internal` std packages is therefore a **policy decision** (§4.2), not a
bug fix.

### 2.3 Face 3 — native builds through `cmd/go` (both modes)

Two native builds happen per root and both go through `cmd/go`, whose
`disallowInternal` (`load/pkg.go:1470`) applies the directory/module rule
to the importing package:

- **the bridge worker** (`interp/bashpp_native_bridge.go: begin`): the
  generated `bashpp-session-*.go` (std imports + one `bpppkgN "path"` per
  native import) is built with `go build -p 2 -overlay=… <virtual path>`
  where the virtual path is inside the module dir
  (`bashpp_import_scratch.go`: "The overlay gives Go a virtual importer
  within the original module/internal"). For a package root the module dir
  is `$WORK/b001` → `cmd/go` refuses `testing/internal/testdeps` and every
  `cmd/*/internal` import.
- **the generated module** (backend `compiled` plan): `go build -C
  $WORK/b001/bashpp` of `main.go` importing the closure natively → the same
  refusal (the story brief's `could not import cmd/compile/internal/amd64`).

Darwin pre-check (recorded in the spike): a `package main` importing both
`testing/internal/testdeps` and `cmd/compile/internal/abt` is refused by
`go build` (`use of internal package … not allowed`) and **builds, links and
runs** through `go list -export -deps -f 'packagefile {{.ImportPath}}=
{{.Export}}'` → `go tool compile -p main -importcfg` → `go tool link
-importcfg`. The compiler is policy-free; `go list` does not apply the
internal rule to command-line targets (`load/pkg.go:1503`, "Anything listed
on the command line is fine"). This is the `-importcfg` half of
`docs/bashpp-import-resolution.md` §1, applied to the *native* side.

## 3. What is already there (and honest)

`gosource.Load` with `Options.Packages` (S149.11 + S151.1): every mapped
package is parsed and type-checked from its exact bytes in map order
(`mapImporter.checkDependency`), registered under its `-p` path, then
**lowered into the one flat program** ahead of the program's own
declarations with hygiene-prefixed names (`mangleLinkedNames`, imports
hoisted as `__gosource_import_<pkg>_<file>_<i>`), initializers in Go order.
The interpreter runs that flat program; its `import` statements are only
the *non-mapped* paths, which the native bridge resolves (Face 2) and
serves from one persistent worker (Face 3). `--go-list` prints each
resolution with `origin: package-map | importer`.

That is the honesty rule as it stands and it is the right one: **the tested
package's Go (its files, its in-package tests, its external tests) is in
the map and is executed by Bash++'s interpreter; its dependency closure is
native through the bridge.** For a package root the closure is `testing`,
`os`, `fmt`, … and, for compiler packages, `cmd/compile/internal/base`,
`cmd/internal/obj`, `internal/buildcfg`, … — none of which is the tested
source. Nothing here is a fallback: the original tested files are never
compiled natively in interpreted mode (the map replaces them), and the
verifier can prove it from `--go-list` (every tested path
`origin=package-map`) and from the bridge worker's import table (no tested
path in it).

The flat program is also what `transpile` emits in compiled mode
(`lower.Options.Importer = goProg.Importer`, `bashy/internal/agentos/
transpile.go:466`), which is why §5's flat route exists.

## 4. (a) Interpreted execution of the explicit package map — what is missing

Three mechanisms, one policy decision, and a cost the mechanisms cannot
remove.

### 4.1 Identity-keyed `internal` visibility (mechanism, `gosource` + `lower` + `interp`)

One function, `cmd/go`'s rule expressed on identities, used at all three
sites:

```text
visible(importer identity, imported path):
  no "internal" element in path                      → yes
  parentOfInternal := path[:index of last "internal"]
  importer is the test main (explicit)               → yes for testing/internal/…
  importer identity has prefix parentOfInternal      → yes   (cmd/go module branch,
                                                              load/pkg.go:1561)
  parentOfInternal == "" (top-level internal/…)      → yes iff importer identity is
                                                       standard (first element has no dot,
                                                       cmd/go's IsStandardImportPath)
  otherwise                                          → no
```

- **identity** = the `-p` path the map carries: `PackageSpec.Path` while
  checking a mapped package (`mapImporter.from`), `Options.ImportPath`
  for the program. Without a map (ordinary programs) the identity stays
  what `determineCallerPath` derives today, so nothing changes for a module
  on disk. `srcDir` (the importing file's directory) is the second signal
  `go/types` already supplies and `cmd/go` uses for GOPATH/GOROOT trees.
- **the test main**: `cmd/go` exempts the importer whose stack label is
  `testmain` (`load/pkg.go:1485`); its import path is `<pkg>.test`
  (`load/test.go:314`). Bash++ should receive the fact, not infer it from a
  suffix: `gosource.Options.TestMain bool` / `bashy --go-test-main`, set by
  the backend at the very site where `cmd/go` knows it is running the
  testmain. (An alternative keyed on the `.test` suffix is `cmd/go`'s own
  convention, but explicit beats a name rule.)
- call sites: `moduleImporter.ImportFrom` (take the identity from the
  mapImporter — a small interface `ImportFromPackage(path, identity,
  srcDir)`; or let `mapImporter` decide visibility itself and hand the
  fallback a policy-free lookup), `nativeBashPPEvaluator.Resolve`
  (`validateBashPPImportVisibility` → identity rule), and the worker build
  (§4.3, which makes `cmd/go`'s own check moot).

Reproducer shape (outside corpus, `gosource/testdata/sprint162/internal-
visibility/`): a mapped package `example.com/m/internal/x` imported by
`example.com/m/y` (positive), by `example.com/other` (negative: refused
with gc's wording), by a program with identity `std`-shaped importing a
top-level `internal/…` (positive) and with a dotted identity (negative),
and `testing/internal/testdeps` with and without `TestMain` (positive /
negative).

### 4.2 The reviewed inventory (policy decision for the manager/user)

`BashPPStdlibImportAllowed` must not be widened to "anything standard".
The narrowest honest extension: **an `internal` std package is admitted
only when §4.1 grants it to the program's declared identity** — which no
user program can hold (a user identity is dotted, hence never standard,
hence never inside `cmd/`, `testing/`, `internal/`). The public inventory
stays as reviewed; the extension is reachable only through an explicit
`--go-import-path` inside the std/cmd tree, i.e. only by a tool driving
Bash++ with an explicit map. This changes a reviewed boundary and must be
recorded as a decision (the plan's "one SDK policy" §3.4 of
`bashpp-import-resolution.md` is the neighbouring open item).

### 4.3 The bridge worker built with `importcfg` (mechanism, `interp`)

Replace `go build -p 2 -overlay=… <virtual path>` in
`bashPPNativeSession.begin` by the policy-free build the compiler was
designed for: `go list -export -deps -f 'packagefile …' <imports>` in the
module dir (one command, same cache work as `go build`), then `go tool
compile -p main -importcfg`, `go tool link -importcfg`. The worker imports
only std + the native paths (`bashpp_native_worker.go.txt`), so the
importcfg is complete. The scratch/overlay machinery of
`bashPPImportTempSource` becomes unnecessary for the worker (the
`importcfg` route needs no virtual path), which also removes the
"helper overlay would mask an existing source path" class. The Go binary
stays `bashPPGoBootstrap`'s reviewed toolchain; its GOROOT carries `src/cmd`,
so `go list -export cmd/compile/internal/base` builds export data there.

### 4.4 The cost the mechanisms cannot remove

After §4.1–4.3 a package root's interpreted row is: the interpreter
executing the tested package under the native `testing` package, with each
`Test*` function a Bash++ closure called back from the worker
(`gosource_callback_bridge.go`). The spike (§6) measures exactly that shape
on `abt` — the first failure is `avlint32.go:104:3: BASHPP-EASSIGN-TYPE:
untyped result is not assignable to *__gosource_pkg_0_node32` (an S162.1
evaluator row on a mapped package's mangled pointer type), before any
timing question arises. Then the bound: `cmd/go` kills the test at 10 min;
the native times in §1 give the headroom. The interpreter's per-call cost
is S162.3-B's number; whatever it is, `cmd/internal/testdir` (256 s
native, spawning thousands of compiles), `ssa` (129 s, 467k LOC),
`cmd/compile/internal/test` (84 s, drives the compiler), `loopvar` (46 s),
`importer` (31 s) and `cmd/compile` (24 s, *is* the compiler's main
package) cannot be interpreted inside 10 min at any plausible slowdown ≥ 3×.
Those six are interpreted design limitations to record by ID. The other 20
are 5 ms – 19 s native; each needs the evaluator to survive its package
(S162.1 mechanism rows will surface there, one package at a time, the same
way testdir rows do).

### 4.5 What the `bashy` CLI needs

Nothing for the map itself — `--go-package`/`--go-import-path` already
execute (S151.1). Only the `--go-test-main` flag of §4.1 (and the backend
passing it). No new resolver, no new policy, no filesystem probing — the
same shape S149.11 shipped.

## 5. (b) The compiled path for `internal` imports

Three candidates, judged on "the generated Go must come from Bash++, never
the original files" and on whether `cmd/go` will build it.

### 5.1 `-overlay` placing generated files at the original import path (recommended)

Bash++ lowers the tested package **as a library**: same package clause,
one generated file per original file (the emitter knows every
declaration's origin — each carries a `//line <file>:<line>` today; the
spike split the flat output by that directive by hand), per-file imports.
The pinned `cmd/go` then runs `go test -overlay=overlay.json <pkg>` where
the overlay replaces `$GOROOT/src/<pkg>/<file>.go` → generated file for
**every** Go file of the package (`GoFiles`, `TestGoFiles`,
`XTestGoFiles`, as `go list -json` enumerates them on the run host). The
result:

- the package keeps its identity and location, so `cmd/go` applies its own
  `internal` rule natively — `internal/buildcfg`, `cmd/compile/internal/*`,
  and `_testmain.go`'s `testing/internal/testdeps` (the testmain exception is
  `cmd/go`'s) all resolve as they do in the native lane;
- `cmd/go` generates and compiles its own `_testmain.go` — the original
  enumeration, verified by the existing `bashppTestCount` read-back;
- the dependency closure stays native (the existing rule); the `.s`
  companions of `ssa` (`flags_{amd64,arm64}_test.s`) are assembled natively
  as they are in the native lane — assembly is a compiler artifact
  (Sprint 152 D4 / 162 D3 shape); to be recorded, not hidden;
- `go vet`'s test-time checks run over the generated files (a vet failure
  is a product row, honestly);
- positions: `cmd/go` passes `-trimpath "<generated>=><original>"` for
  overlaid files (seen in the spike's `-x` output), so diagnostics and
  `runtime.Caller` cite the original path without any `//line` directive.

Proof of "no native tested-source fallback": the backend event records the
overlay JSON (every original path → generated path + SHA-256), `go list
-overlay -json`'s file inventory (must equal the overlay's key set for the
three file classes), and the `-x` compile argv for `-p <pkg>` (positional
inputs all generated). A file of the tested package that is not overlaid is
a seam failure, not a product row. Under the overlay the *only* native
compilation of tested source that could happen is none: the originals are
shadowed for every read `cmd/go` makes (`fsys`).

What it needs:

- `lower` (S162.4 seam): a library emission mode — the package clause is
  already `lower.Options.Package` (default `main`, `lower/compile.go:155`,
  emitted at `:365`; `bashy transpile` does not expose it yet), split
  output by origin file, prune imports per file, keep names (152 D1 already
  keeps a single Go-only input's names — the spike's `abt` output has zero
  `__gosource` names and zero runtime imports), no `Entry`/runtime unit
  (the "runtime-free native unit" of `lower.Options.Entry == ""`). The
  spike's S step (§6) measured this gate on all 26 with the base candidate:
  17 of 25 applicable packages lower as a library today; the residues are
  named product rows (`*ast.ArrayType` forms, a type-switch initializer,
  hoisted-import pruning, one conversion), not packaging rows. Explicit
  conversions and `if x := …` hoisting change the text but not the
  semantics.
- `gosource` (this seam): "mapped package as *import*, not flattened" for
  the external test package — `pxtest` is checked against the map (the
  tested package from its exact files) but emitted with `import "<pkg>"`
  so that, under the overlay, the import resolves to the generated package.
  A one-flag variant of the S151.1 link step (`LinkedPackage` kept as an
  import edge).
- backend seam (`bashpp-tests/testdata/go-backend`): a second disposition
  `transpile-overlay-go-test` — before the test binary is *built* (not at
  the run site): enumerate the package's files through `cmd/go`'s own
  `load.Package` (the seam is inside `cmd/go`), transpile each build unit,
  write the overlay, and hand `cmd/go` the overlay for the build. Two
  equivalent placements: a pre-pass (`go list -json` → transpile →
  `go test -overlay`) driven by `package-gate.sh`, or a patch at the
  package-compile action. The pre-pass keeps the seven-line patch untouched
  and is the smaller change; the run site then runs the binary `cmd/go`
  built from generated code with the backend's argv substitution *off* for
  that root. `package-verify.go` gains the three proofs above.

### 5.2 A generated *module* inside a GOROOT copy / a `-trimpath`'d GOROOT overlay — rejected

The brief's alternative. `disallowInternal` grants `p/internal/q` to
importers under `p` (by directory for GOROOT packages, `load/pkg.go:1537`).
`_testmain.go` needs `testing/internal/testdeps` (under `testing/`), the
tested compiler package needs `cmd/compile/internal/*` (under
`cmd/compile/`) and `internal/*` (under `src/`). **No directory is under
both `testing/` and `cmd/compile/`**, so a generated `package main` module
placed anywhere in a GOROOT copy is refused on one side or the other; only
`cmd/go`'s testmain label escapes, and it is not available to a module. It
also needs `mvdan.cc/sh/v3/lower/shellrt` vendored into the tree
(`src/cmd/vendor` + `modules.txt`) whenever the lowering needs the runtime,
and a full SDK copy per root. Rejected on both counts; recorded so it is
not re-proposed.

### 5.3 The flat program built by `importcfg` — works, fallback

Keep today's transpile shape (map flattened into `main.go`) and replace the
backend's `go build -C moduleDir` by `go list -export -deps` → `go tool
compile -p main -importcfg` → `go tool link -importcfg`, run in the
generated module dir so `mvdan.cc/sh/v3/lower/shellrt` resolves through its
`replace`. Measured on the leaf (§6, C3). It keeps "no native tested-source
fallback" (the tested files are in the map, flattened, never compiled
natively), needs no emitter change, and shares §4.3's build with the bridge
worker. Costs: mangled names in the generated program (irrelevant to a run
verdict, but `-trimpath`/`//line` positions are the only thing citing the
original files); the `.s` root (`ssa`) is unreachable (non-Go inputs cannot
be flattened); the test enumeration is `cmd/go`'s `_testmain.go` already —
unchanged. This is the route if the library emitter of §5.1 turns out
costlier than expected; both can coexist behind one disposition each.

## 6. The spike — `cmd/compile/internal/abt` on the leaf host

Record: `docs/evidence/sprint162-abt-spike.md` (commands, outputs, wall
times; the script is `docs/evidence/sprint162-abt-spike.sh`). Steps: N
(native baseline), R0 (the backend's exact argv on the base candidate), C1
(overlay route by hand), C2 (flat route with a `testing.Main` driver), C3
(flat + `importcfg` with the real `_testmain.go` body), I1 (interpreted,
same driver + map), S (library transpile of all 26 as the compiled route's
first gate). The numbers are in the record; the table below is the summary
the manager needs.

| route | `abt` on the leaf host (2 vCPU, Go 1.27.0, base candidate `548c3a4`) | wall |
|---|---|---:|
| native `go test` (baseline) | PASS, 9 tests (cmd/go's own `_testmain.go`) | 0.39 s warm (19.6 s with a cold std build) |
| backend's exact argv, interpreted **and** compiled | exit 2: `_testmain.go:10:2: could not import testing/internal/testdeps (use of internal package … not allowed)` — Barrier B reproduced by hand | 0.5 s / 0.4 s |
| **C1 compiled, overlay at the original import path** | library transpile (0 runtime imports, 0 mangled names, 58 + 34 decls vs 58 + 33) → split by origin → `go test -overlay`: **PASS 9/9**; `-x` proof: compile inputs 2 generated / 0 original; canary present only in the generated file runs under the overlay and not natively | transpile 0.59 s + `go test` **0.68 s** (0.34 s warm) |
| C2 compiled, flat program (driver = `_testmain.go` with `testing.Main`) + map | transpile → `go build` → run: **PASS 9/9** | 1.17 s + 0.62 s + 0.01 s |
| C3 compiled, flat program with the real `_testmain.go` body (`testdeps`) | `go build`: refused (`use of internal package testing/internal/testdeps`); `go list -export -deps`: 129-line importcfg **including testdeps** on the pinned 1.27; compile/link/run proven end to end on darwin (Go 1.26) — the leaf's first pass hit a spike-script assembly defect, corrected re-run queued (evidence record) | list 0.16 s |
| I1 **interpreted**, driver + map through the interpreter | the check passes, the bridge starts native `testing`/`regexp`, `testing.Main` calls back into interpreted `TestApplicInsert`, which fails at once: `avlint32.go:104:3: BASHPP-EASSIGN-TYPE: untyped result is not assignable to *__gosource_pkg_0_node32` (S162.1 evaluator row); same per test | 7.7 s first run (bridge build), 3.9–4.1 s per test thereafter |
| S library transpile of all 26 (compiled route's first gate) | **17 / 25 applicable exit 0** (0.2–3.6 s; `cmd/compile` 65.5 s on the compiler closure's first `go list -export -deps`); 9 first lines: `gosource: unsupported expression *ast.ArrayType` ×4 (`ssa`, `ssagen`, `types2`, `go/types`), `unsupported type switch initializer` (`noder`), `LOWER-ETYPE "unsafe" imported and not used` ×2 (`ir`, `types`), `LOWER-ETYPE cannot convert *big.Int` (`amd64`); `cmd/internal/testdir` is xtest-only | 13.9 s for 464k LOC (`ssa`) |

The number D1 asked for: **`abt`'s original 9-test set passes on the
Bash++-generated package in compiled mode through the overlay route, 0.68 s
on 2 vCPU (native 0.39 s), with the compile argv and a canary proving the
generated files were the ones compiled.** The flat route passes too (C2).
Interpreted mode reaches the tested code and fails on an evaluator gap, not
on packaging. Across the 26, the compiled route's first gate is already
17/25 green on the base candidate without any emitter change; the 9
residues are named product rows (4 `*ast.ArrayType` gosource forms → S162.1
compiled class; 3 `LOWER-*` → S162.4; 1 type-switch initializer → gosource
owner; 1 xtest-only package → the §5.1 import edge).

## 7. Cost across the 26 and the recommendation

### 7.1 Per option

| Option | Mechanisms (seam) | Roots it can reach | What stays FAIL by decision |
|---|---|---:|---|
| **Compiled — overlay (§5.1)** | library emitter: package clause, per-file split, per-file imports (`lower`, ~1 mechanism + fidelity rows); xtest as import edge (`gosource`, small); `transpile-overlay-go-test` disposition + 3 proofs (backend seam + `package-verify.go`) | **26** by construction — each root's residue is then a lowering fidelity row (152 D1 machinery) or a real product difference | none by construction; `ssa`'s two `*_test.s` assembled natively must be recorded (D3 shape) |
| Compiled — flat + importcfg (§5.3) | importcfg build in the backend (Bash only, seam) + §4.1 visibility | 25 (`ssa` unreachable: `.s`) | `ssa` |
| Compiled — GOROOT copy (§5.2) | — | 0 | all |
| **Interpreted (§4)** | identity-keyed visibility + `TestMain` flag (`gosource`/`lower`/`interp`/`bashy`, 1 mechanism); inventory policy (decision); importcfg worker build (`interp`); then S162.1/S162.3 rows per package | ≤ 20 in principle (native ≤ 19 s); realistically the small pure-Go packages first (`abt`, `compare`, `logopt`, `internal/types/errors`, `base`, `devirtualize`, `liveness`, `loopvar`'s package, `rangefunc`, `reflectdata`, `types`, `typecheck`, `dwarfgen`, `inlheur`) — each gated by evaluator survival | ≥ 6 by time (`testdir` 256 s, `ssa` 129 s, `test` 84 s, `loopvar` 46 s, `importer` 31 s, `cmd/compile` 24 s: no interpreter slowdown fits 10 min) and every package the evaluator does not survive in-sprint |

Engineering cost, honestly: the overlay route is three bounded mechanisms
in three seams (lower emitter mode; gosource import edge; backend
disposition + verifier), each with an outside-corpus reproducer — about
the size of one S162.4 wave. The interpreted route is one small mechanism
(visibility), one policy decision, one build change in the bridge, and
then an open-ended evaluator campaign whose denominator is 20 packages of
compiler source; its first row on the smallest package is already an
evaluator row.

### 7.2 Recommendation for D1

**(a′)**: authorise the compiled overlay route for all 26 (§5.1) and the
interpreted mechanisms of §4.1–4.3 with the inventory decision of §4.2
recorded explicitly; interpreted credit is then earned package by package
under S162.1/S162.3 and the six time-bound roots are recorded by ID as
interpreted design limitations now, not after a leaf shows them at 10 min.
If the manager does not want the inventory boundary moved this sprint,
(b) — compiled only via §5.1, interpreted recorded by ID for all 26 with
§4 as the recorded cost — loses nothing the compiled lane can show and
keeps the reviewed inventory untouched. §5.2 is off the table.

## 8. Decisions and requests

- **Decision (D1)**: §7.2.
- **Decision**: the reviewed-stdlib inventory extension of §4.2 (security
  boundary; user-level).
- **Decision**: `ssa`'s `*_test.s` companions assembled natively under the
  overlay route — record as the D3(b) disposition for package roots.
- **Request → lower owner (S162.4)**: the library emission mode of §5.1
  (package clause, per-file split by origin, per-file import pruning; the
  `ir` `"unsafe" imported and not used` row is its first reproducer).
- **Request → backend seam (S162.4 lane)**: `transpile-overlay-go-test`
  disposition, overlay/inventory/argv proofs in `package-verify.go`;
  importcfg build for the flat fallback.
- **Request → harness owner (S162.0)**: the package lane's bound is
  `-timeout=10m`, not 60 s — state it in the manifests' rule text so the
  "never a timeout raise" rule is applied to the right number; the
  symlink-mirrored GOROOT sensitivity of §2.1 is a venue fact worth a line
  in `backend.md`.
- **Request → S162.1 integrator**: `avlint32.go:104:3 BASHPP-EASSIGN-TYPE`
  on a mapped package's pointer type — the first interpreted row of the
  package family, reproducible with the spike's driver (§6).
- **Request → S162.3-B**: the per-call cost number applied to the §1
  native times decides which of the 20 small packages can be interpreted
  inside 10 min.
