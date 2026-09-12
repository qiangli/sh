# Sprint 153 — S153.4b interpreted evaluator semantics

Story-ID: e58cccba74f8. Every root below was run natively (`go run`) and
through the interpreter (`bashy.real --bashpp --source=go --go-file …`,
`rundir` roots with `--go-package test/<pkg>=<file> --go-import-base test`),
reduced to an outside-corpus program, and either fixed by a general
mechanism or left open with the reduced program under `open/`.

Fixed mechanisms have their reproducer (plus a positive control) under
`interp/testdata/sprint153/<mechanism>/`, run by `TestSprint153Evaluator`
(native oracle vs interpreter, exact stdout/stderr/status). Module-shaped
mechanisms (multi-package) are `go.mod` directories run through
`gosource.Load`. Diagnostic-only mechanisms are pinned by
`TestSprint153FatalDiagnostic*`.

Files touched outside the lane's listed ownership but unclaimed by the
other lanes: `bashpp_readonly.go`, `bashpp_p1.go`, `bashpp_concurrency.go`
(task failure text only), `api.go` (two runner fields),
`gosource_{values,arguments,collection_values,nil,calls}.go`, and the new
`gosource_{method_expr,method_package,struct_identity}.go`.

## Roots

| Root | Native | Interpreted before | Mechanism | Status |
|---|---|---|---|---|
| `reorder.go` | 0 | `BASHPP-EASSIGN-ARITY` at p9's `x, x = m[0]` (the manifest's `[1 100 3]` row no longer reproduces; every pN passes) | comma-ok map index / assertion in plain `=`; `true`/`false` as interface initialiser; asserted scalar loses kind | fixed: b2ab06b0, 04c3e935, b219700f |
| `fixedbugs/issue4167.go` | 0 | exit 2, no diagnostic | multi-result call spread into arguments; computed callee in statement position; method expression `(*T).M` / `T.M` as forwarding closure; `(*p)(&n)` pointer conversion | fixed: 5073decb, 994f9f2c, e144401f, 67227737 |
| `fixedbugs/bug367.go` (rundir) | 0 | `panic: should not satisfy main.I` | unexported interface method identity is package-qualified (package read from the linked-package marker) | fixed: 709b86a9 |
| `fixedbugs/issue13160.go` | 0 | `scalar call interrupted` | `ps[i] = new(T)` delivered as a call (fixed: 70464b34); then a torn read: the program races a writer and a reader on one `[]*int` slot; interpreter slots are two-word interfaces and `expand.NewObject`'s `json.Marshal` walk panics on a half-written one | **open** — `open/issue13160_torn_read.go`; needs element storage that is word-atomic or a validator that does not traverse shared storage (`expand/`, not evaluator) |
| `fixedbugs/issue45045.go` | 0 | `scalar call interrupted` | `unsafe.Pointer`/`reflect.StringHeader` rewriting and `runtime.SetFinalizer` on interpreter-owned storage | **open** — no memory model to emulate; not general |
| `fixedbugs/issue24491b.go` | 0 | `scalar call interrupted` | `unsafe.Pointer`↔`uintptr` liveness with finalizers and `runtime.GC` | **open** — same class |
| `typeparam/issue48042.go` | 0 | `undefined type: T`, `scalar call interrupted` | explicit type argument `g[T]()` bound through the frame; interface owning a func literal; `return new(T)` delivered as a call | fixed: d5c0079d, 59cad25f, 70464b34 |
| `fixedbugs/bug510.go` (rundir) | 0 | `scalar call interrupted` | typed nil pointer `(*A)(nil)` as a dependency-call argument, and `reflect.New(...)` producing a value of a local alias type | **open** — bridge (`bashpp_native_*`); `open/bug510_typed_nil_native_arg.go` |
| `typeparam/cons.go` | 0 | `scalar call interrupted` ×3 | returned/passed type assertion keeps the asserted cell; a call's result as an interface source (generic struct field `Tail List[a]`) | fixed: 4b84e70e, a94d658a |
| `alias1.go` | 0 | `panic: byte != uint8` | `byte`≡`uint8`, `rune`≡`int32` in dynamic identity and assignability; `flag = true` (predeclared booleans on an assignment's RHS were looked up as variables) | fixed: 5792b830, 14091eb6 |
| `fixedbugs/issue42401.go` (rundir) | 0 | `panic: FAIL` | `//go:linkname s test/a.s` aliases a variable across packages | **open** — compiler directive; not interpretable |
| `fixedbugs/issue47087.go` (rundir) | 0 | `BASHPP-ECOMPARE-NONCOMPARABLE` | struct literal type identity (fields + package-qualified unexported names); run-time panic for comparing an uncomparable identical dynamic type | fixed: 7531fea2, a2e13d04 |
| `fixedbugs/issue52856.go` (rundir) | 0 | `panic: 0` | same struct literal identity (embedded `int` is unexported, hence package-qualified) | fixed: 7531fea2 |
| `fixedbugs/bug260.go` | 0 | `FAIL`, exit 1 | `%p` of consecutive array elements must differ by the element's size/alignment | **open** — the interpreter has no address arithmetic; pointer formatting is the bridge's synthetic identity |
| `fixedbugs/gcc65755.go` | 0 | exit 2, no diagnostic | function-local type declarations are frame-scoped (fixed: 55f19b96); remaining: the bridge keys local struct codecs by bare name, so two `s` shapes collide in the worker | **open** — bridge; `open/gcc65755_local_type_codec.go` |
| `fixedbugs/issue24491a.go` | 0 | exit 2 | `unsafe.Pointer`↔`uintptr` with finalizers | **open** — same class as issue24491b |
| `fixedbugs/issue69507.go` | 0 | exit 2, no diagnostic | `range f()` over a call's result; `i := 1` in a function whose first argument is a closure bound `$1` (a literal looked up as a function name) | fixed: 7c87a542, e71adf50 |
| `typeparam/issue48645a.go` | 0, prints `func(func(int) bool)` | exit 2, no diagnostic | fatal diagnostic dropped across `return f()` (fixed: d20b6050); remaining: a closure handed to `reflect.TypeOf` | **open** — bridge (function values do not cross); `open/issue48645a_closure_to_reflect.go`; now reports the reason and exits 1 |
| `typeparam/boundmethod.go` | 0 | `cannot convert Int to Stringer` | `Stringer(v).String()` — interface conversion as a value (fixed: e3810745); remaining: `reflect.DeepEqual` on local slices | **open** — bridge slice policy; `open/boundmethod_deepequal_slices.go` |
| `fixedbugs/issue10353.go` | 0 | (passes at this revision) | — | passes |
| `fixedbugs/issue16331.go` | 0 | `bash++: task failed: exit status 1` | a task's fatal diagnostic reported as its failure (fixed: 97ad1048); remaining: typed nil func `(func())(nil)` to `reflect.TypeOf`, `reflect.MakeFunc` over an original function, and an original method value through reflect | **open** — bridge; `open/issue16331_method_value_transport.go` |

## Needs for other lanes (not edited here)

* Bridge (`bashpp_native_*`): typed nil pointers/funcs as native arguments
  (bug510, issue16331); local struct codecs keyed by bare name collide for
  same-named function-local types (gcc65755); `reflect.DeepEqual` on local
  slices (boundmethod); function values to `reflect` (issue48645a,
  issue16331); a local struct with an interface-typed field is not emitted
  for the worker (`undefined: Shape`, met while writing
  `interface_call_source` — reproducer kept generic-only for that reason).
* `expand/`: `NewObject` validates by walking the payload with
  `json.Marshal`; under a program-level data race on a slice slot this
  panics inside the interpreter (issue13160).
* `gosource/`: a first-class package tag on declarations and type literals
  would replace the `__gosource_pkg_N_` marker reads in
  `gosource_method_package.go` (an interface method's package is that of
  the *declared* interface it is spelled in; an anonymous interface literal
  reads as the program package) and `gosource_struct_identity.go` (a struct
  literal's package is read from its source's top-level declarations; a
  source with none reads as the program package). `syntax.Printer` panics
  on a method with an unnamed receiver (`func (S) private()`), met while
  dumping merged programs.

## Commands

```sh
WS=$(pwd); U=<umbrella checkout holding the sibling bashy repo>
awk -v ws="$WS" '{sub(/=> \.\.\/sh$/, "=> " ws)}1' $U/bashy/go.mod > $WS/.bashy-build.mod; cp $U/bashy/go.sum $WS/.bashy-build.sum
(cd $U/bashy && go build -modfile=$WS/.bashy-build.mod -o $WS/bashy.real ./cmd/bashy)
$WS/bashy.real --bashpp --source=go --go-file <root.go>
$WS/bashy.real --bashpp --source=go --go-file <dir>/main.go --go-package test/a=<dir>/a.go --go-import-base test
PATH=/bin:/usr/bin:$(dirname $(which go)) go test -count=1 -timeout 30m -run GoSource ./interp/...   # baseline: the 8 known darwin failures
PATH=/bin:/usr/bin:$(dirname $(which go)) go test -count=1 -timeout 30m ./interp/...
PATH=/bin:/usr/bin:$(dirname $(which go)) go test -short -timeout 30m ./...
```
