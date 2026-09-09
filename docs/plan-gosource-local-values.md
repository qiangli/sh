# Go-source local structured values, receivers and interfaces — Sprint 118

Story #54 (`c3a60493cde9`). Scope: make the Go-source runtime hold local
structured values coherently — struct fields, receivers, pointers, defined
scalar types and interface values — without compiling the whole Go program and
without rewriting the original source.

## Ownership taken

Primary: `interp/bashpp_struct.go`, `interp/bashpp_interface.go`,
`interp/bashpp_p1.go`, and the method/receiver/short-declaration result paths
of `interp/bashpp_func.go`.

Reached beyond that only where a defect's single call site lives elsewhere, and
only as a delegation to a helper in an owned file:

| File | Change | Why |
| --- | --- | --- |
| `interp/bashpp_embed.go` | selector lookup consults every name of a grouped field declaration | the one call site of the field-name helper |
| `interp/bashpp_pointer.go` | `&T{…}` delegates to `bashPPCompositeAddress` | address-of is the struct value's own surface |
| `interp/bashpp_scalar.go` | three call sites: typed call arguments, method-value callees, `T(v)`/`i.(T)` as values | each delegates to an owned helper |
| `interp/bashpp_readonly.go` | assignment consults the interface/assertion candidate helpers | interface-typed assignment targets |
| `interp/bashpp_collection.go` | one call to `bashPPRepresentableScalar` | untyped constant stored in a struct field |
| `interp/gosource_calls.go` | tuple call arguments reuse `bashPPTypedCallArgs` | removes a duplicate of the same argument loop |

Nothing in `gosource/`, `lower/` or `syntax/` was touched.

## Corpus movement

`tour/_content/tour/methods` + `moretypes`, interpreted mode, measured against
frozen baseline 002.

Passing before: `struct-fields`, `array`, `slices` (3).
Passing after: those plus `methods`, `methods-continued`, `methods-funcs`,
`methods-pointers`, `methods-pointers-explained`, `indirection-values`,
`interfaces-are-satisfied-implicitly`, `empty-interface`, `type-switches` (12).

`examples/custom-errors` no longer panics with a nil dereference in
`bashPPShortDeclCall`; it now reaches, and stops at, the native bridge.

Every remaining row in this area is blocked on a surface another worker owns —
see below. No row regressed.

## Remaining gaps, by owner

**Native dependency bridge (bridge31)** — the largest remaining block. Printing
or passing a structured value to an imported package fails:
`unregistered bridge type "Vertex"` / `"I"` / `"Person"` / `"error"` /
`"[]struct{…}"`, `integer for uint8`, and
`unknown imported symbol or method: …AsType`. This blocks `structs`,
`struct-literals`, `struct-pointers`, `indirection`,
`methods-with-pointer-receivers`, `interface-values`,
`interface-values-with-nil`, `nil-interface-values`, `stringer`,
`exercise-errors`, `reader`, `slice-literals` and `custom-errors`, all of which
are otherwise correct up to the bridge call. `errors.go` additionally needs
`time.Time` as a declared field type.

**Go front end (frontend32)** — a multi-value `return` carries only word
spellings: `syntax.BashPPReturn` has `Results []*Word` plus a single `Expr`, and
`gosource/convert.go` populates `Expr`/`Call` only when `len(x.Results) == 1`.
So `return arg + 3, "s"` hands the interpreter the literal text `arg + 3`, and
`n, m := f(1)` binds `n` to that text. This is pre-existing (frozen baseline 002
fails it identically) and cannot be fixed from `interp`: it needs a
`ResultExprs []BashPPExpr` field on `BashPPReturn`, populated for the
multi-value case and walked/printed alongside `Results`. Once it exists,
`bashPPReturnStmt` can route each result through the same
`bashPPStructuredArgCell` path the single-result form already uses, which is
what `custom-errors` (`return -1, &argError{…}`) and `methods/errors.go` need.

**Collections (unassigned in this slice)** — `nil-slices`, `making-slices`,
`append` fail with `gosource: unsupported interpreter collection value <nil>`;
`slice-len-cap` and `slices-of-slice` need `BashPPSliceExpr` and structured
index reads as values. A nil slice must retain its type and zero state rather
than becoming an untyped nil.

**Concurrency / signals (worker30)** — `interp/signal.go` never stops the
subscription goroutines started for `WithStandaloneSignalDefaults`, so they
accumulate across repetitions inside one test binary. `go test ./interp` (the
CI mode) is green; repeated runs in one binary trip
`TestConcurrencyScheduleMatrix`'s goroutine-leak assertion once the run is long
enough for enough of them to age past the detector's window. It is purely a
function of run length: frozen baseline 002 passes at `-count=3` and fails at
`-count=5`, this tree passes at `-count=1` and fails at `-count=3`, and the
leaked goroutines are all `forwardSignalSubscription` workers. Adding any test
to the package moves the threshold — the defect is the subscription lifecycle,
not the tests that surface it.

## Verification

- `go test ./interp ./gosource ./syntax ./expand ./shell ./pattern ./fileutil ./lower` — green.
- `go test -race ./interp -run 'TestGoSourceStructuredValues|TestBashPPStruct|TestBashPPInterface|TestBashPPMethod|TestBashPPPointer'` — green.
- `TestGoSourceStructuredValuesMatchGo` (`interp/bashpp_method_gosource_test.go`)
  runs five unchanged Go programs through both the Go toolchain and the
  interpreter and requires identical output. It prints only scalars on purpose,
  so it reports on this slice rather than on the bridge.
