# Go-source collections: conversion, growth and aliasing — Sprint 118

Story #1 (`2daf9ef04ad4`). Continuation of collection worker run37, whose budget
expired. This slice takes the "Collections (unassigned in this slice)" block
left open by [`plan-gosource-local-values.md`](plan-gosource-local-values.md).

Seeded from manager runtime `194d246e` ("interp: preserve the GoSource caller
environment for native dependencies"). Run37's preserved commit `6fe6bfc2` was
cherry-picked onto it; it applied without conflict, because the calls/function
work it was written against (`27eff888` in run37's branch) is present in `194d`
as `bbaf86e9`. Nothing from `194d` was reverted: `TestGoSourceTestingOriginalErrors`,
the call/function-value results and the typed-float work are all preserved.

## Ownership taken

Primary: `interp/bashpp_collection_convert.go`, `interp/bashpp_collection_growth.go`
and `interp/bashpp_collection_bridge.go` (new in this slice).

Reached beyond that only as a delegation to a helper in an owned file:

| File | Change | Why |
| --- | --- | --- |
| `interp/bashpp_builtin.go` | `append` gathers its elements and grows through one call; `make` zeroes spare capacity; `len` of a nil map | the builtin dispatcher is the single call site of the growth helpers |
| `interp/bashpp_native_values.go` | bridge consults the structured read, the nil-collection description and the value-builtin hook | three delegations ahead of the scalar fallthrough |
| `interp/bashpp_func.go` | `f([]byte(s))` and `f(xs[1:])` pass the collection they are | argument cells for conversion and slice expressions |
| `interp/bashpp_p1.go` | `bs := []byte(s)` binds the slice | short declaration of a collection conversion |
| `interp/bashpp_scalar.go`, `interp/bashpp_readonly.go` | conversion/assignment consult the owned helpers | single call sites |

Nothing in `gosource/`, `lower/` or `syntax/` was touched. No original source is
rewritten and no original function body is forwarded to native Go.

## How the native comparison is done

`differGoSource` (in `bashpp_native_structured_test.go`) is the only oracle used:

1. write the unchanged original source to a temp dir;
2. `go build <file>` — naming the file **explicitly** is what lets the pinned
   Tour originals keep their `//go:build OMIT` line, because cmd/go does not
   apply build constraints to files handed to it directly. The constraint is
   never stripped and the source is never edited;
3. execute the resulting binary and capture **complete** stdout, **complete**
   stderr and the exit status — nothing is filtered, trimmed or normalised;
4. run the same bytes through `gosource` + `interp` and require all three to
   match;
5. re-read the source file afterwards and require it to be byte-identical.

The vendored Tour originals under `testdata/gosource-collections/` are pinned by
SHA-256 in `sha256.json`, checked before each run. This replaces the earlier
`/tmp/s118sweep.sh` sweep, which stripped `//go:build OMIT` and filtered `go run`
streams; results from that sweep are invalid and are not carried forward.

## Results

`go test ./interp -run 'TestGoSourceCollections$|TestGoSourceCollectionConversions|TestGoSourceSliceGrowthAndAliasing|TestGoSourceNilCollections'` — green.

Pinned upstream Tour originals passing (8 of 9): `append.go`, `making-slices.go`,
`nil-slices.go`, `slice-bounds.go`, `slice-len-cap.go`, `slices.go`,
`slices-of-slice.go`, `slices-pointers.go`.

Exact cases passing: byte/rune slice conversion and the round trip back to a
string; append within capacity writing through to the parent; append past
capacity detaching; two slices over one array; `append(a, b...)` leaving its
source alone; the nil slice's value, length, capacity, `== nil`, `%T`, range and
first append; a nil collection crossing to `strings.Join`/`sort`.

Defects fixed in this slice, both found by the differential tests above:

- `len(m)` on a nil map **crashed the interpreter** with
  `interface conversion: interface {} is nil, not map[string]interface {}`.
  A nil map is a map with no entries, so its payload is absent rather than an
  empty table (`bashpp_builtin.go`).
- a predeclared value call used as a dependency argument —
  `fmt.Println(append(s[:0], "a"))`, `fmt.Println(copy(dst, src), src)` — failed
  with `BASHPP-EEXPR-UNDEFINED: undefined callable append`. These builtins are
  implemented over cells rather than as callables, so the scalar evaluator had
  nothing to look up; the bridge now runs them once, in place
  (`bashpp_collection_bridge.go`). Running them *once* matters: `copy` mutates,
  so a hook that probed for a collection and then re-ran for a scalar would copy
  twice.

## Remaining failures, measured not skipped

Held in `interp/bashpp_collection_gate_test.go` behind `-tags gosource_collection_gate`,
the same convention `gosource_testing_corpus_test.go` uses for its corpus gate.
Nothing is skipped, capped or normalised — each row runs through the same oracle
and turns green by itself once the surface below lands.

	go test -tags gosource_collection_gate ./interp -run GoSourceCollectionGate

**Native dependency bridge (bridge31)** — the largest remaining block, and the
same one `plan-gosource-local-values.md` reports. Printing a struct or a
collection of a script-declared type to a dependency fails with
`unregistered bridge type "bag"` / `"[]point"` / `"[2]point"` /
`"[]struct{i int;b bool}"` / `"[]Byte"`. This blocks the pinned
`slice-literals.go` and every `GateStructElements` row. The interpreted result is
correct up to the bridge call in each case.

**Indexed assignment target for a predeclared value call** — `rows[i] = make([]int, 3)`
and `m["a"] = append(m["a"], 1)` report
`BASHPP-EBUILTIN-TYPE: assignment target "rows[i]" is not declared`. The bridge
hook added here covers the *argument* position; the assignment-target position
needs the same treatment in `bashpp_readonly.go`, which this slice ran out of
budget for.

**Growth capacity from a zero-capacity slice** (`growth_capacity_by_element_type`) —
Go 1.27 answers the first append to a nil `[]int` with cap 4, to `[]string` with
cap 2 and to `[]bool` with cap 32; the interpreter answers 1, 1 and 1, then
tracks the `[]any` payload's own doubling. `bashPPSliceGrowCap`'s reflect probe
is not being consulted on the zero-capacity growth. Note this is *not* uniform in
real Go either: the pinned `append.go` natively reports cap 1 for the same
first-append-to-nil-`[]int`, and that row passes. The discrepancy between those
two native results is unexplained and is the first thing to settle here — it
must be understood before the probe is wired into the zero-capacity path, or the
fix will be tuned to one of the two observations.

**Byte-slice conversion gaps** — `append(bs, []byte("ef")...)` spreads the
conversion's *spelling* rather than its bytes (`abcd[]byte("ef")`), and
`type Byte = byte` is not resolved through the alias by `bashPPByteOrRuneSlice`.
Both are this slice's own surface and are the next work here.

**Return of a predeclared value call** — `func count(bs []byte) int { return len(bs) }`
reports `BASHPP-ERETURN-CALL: return requires a declared callable`. Same class as
the argument-position hook, in the return path.

**Panic message fidelity** — `nil_map_write_panics` emits
`BASHPP-ENIL-MAP: assignment to nil map` where Go emits
`panic: assignment to entry in nil map` plus a goroutine trace. Exit status
already agrees (2). Panic formatting is a separate surface (see
`docs/bashpp-p3d-panic-decision.md`), not a collection defect.

## Baseline

The classic external Bash fixtures have known pre-existing failures unrelated to
this slice; they are reported separately and no product code was skipped or
capped to accommodate them.
