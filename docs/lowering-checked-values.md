# Checked native value operations

This change provides runtime checks and emitter hooks. The main compiler still
owns dispatch, static type analysis, and Program.Run integration; helper tests
alone do not establish completion of the Go profile.

## Runtime contract

`ValueError` carries `Code`, `Message`, and `ValueSite` (`File`, `Name`, `Line`,
`Column`, `Offset`). `Error()` returns the engine's exact diagnostic text;
`ExitStatus()` returns 2. Source metadata remains available without adding an
unrequested CLI prefix. Helpers neither print nor use global runtime state.

`MustValue` and `MustAssertOK` raise a typed `ValueAbort`, which implements
`error`, `Unwrap`, and `ExitStatus() int`. The owning Program boundary recovers
this failure, reports it once, and stops execution. Language-level `recover`
must let this internal unwind pass through. `AsValueError` retrieves the original
positioned error from either the direct error or the unwind.

`CheckedPointer` returns the same non-nil pointer, making checked dereference
usable on both sides of an assignment. `Deref` copies a native Go value, so array
and struct values copy while slices, maps and pointers retain their normal
aliasing. The emitter leaves these calls within their original expressions;
logical short circuiting must not hoist an unneeded check.

`Assert` and `AssertOK` use Go's native generic assertion. They preserve dynamic
type and method set identity, including a typed nil pointer in an interface.
A failed comma-ok assertion returns the target's native zero and false. A nil
interface fails a single assertion. `Assertion.Impossible` is explicitly
provided by compiler analysis and rejects both forms with
`BASHPP-EASSERT-IMPOSSIBLE`. Runtime reflection only supplies a diagnostic type
name when the compiler has not supplied one; it never decides impossibility or
reconstructs a value from shell text or JSON.

`CheckIndex` accepts all native integer widths without narrowing overflow.
`checkedIndexRead` captures a sequence and then an index once. For an addressable
array, the compiler must pass its address; copying an array before an index with
side effects would read stale elements. Indexed assignment must capture the
addressable container and index, use `checkedIndexOffset`, then perform the
native assignment. Pointer-to-array implicit dereferences need a pointer check
at that seam. Slicing bounds and non-integer index diagnostics remain separate
compiler work.

`MakeSlice` validates negative lengths and capacity smaller than length.
`MakeSliceLength` handles an omitted capacity without repeating evaluation of
the length. `MakeMap` validates negative size hints. These helpers preserve native allocation
and zero values; they do not attempt to recover process memory exhaustion.
For a named slice or map, the compiler must retain its named result type with a
native conversion around the helper result. Native `len`, `cap`, `append`,
`copy`, `clear` and `delete` retain their ordinary valid-input semantics; this
change does not provide a generic panic-to-diagnostic adapter.

## Verification

Tests compile helper-generated native artifacts for the unchanged public
`nil-deref-neg`, `assert-fail-neg`, `assert-impossible-neg`, and
`value-copy-assertion-zero` sources. The binaries run after their generated source
files are removed, with no shell tools on PATH. Their stdout, stderr and exact
status are compared with live interpretation of the public sources.
`BASHPP_PROFILE_DIR` optionally verifies the embedded public cases against an
external profile checkout before execution.

An additional generated artifact checks lazy pointer evaluation, source-order
single evaluation, and array mutation during index evaluation. Runtime tests
cover interface and pointer identity, typed nil and nil interfaces, comma-ok zero
values, integer overflow bounds, invalid allocation sizes and typed error unwind.
