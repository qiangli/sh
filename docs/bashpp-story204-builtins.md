# Bash++ Story 204 — predeclared builtins

Sprint 116 · story `97322d0e5504` · 2026-09-06

This slice implements `len`, `cap`, `append`, `copy`, `delete`, `clear`,
slice/map `make`, `min`, `max`, `print`, and `println` over the runtime's single
cell/value model. `new(T)` remains integrated through its existing positioned
typed expression. Channel `make` and `close` remain owned by the concurrency
nodes and runtime; `panic` and `recover` remain owned by the unwind runtime.

Collection results retain their declared named (including instantiated generic)
type in metadata. Slice append reuses backing storage and readonly identity when
capacity permits, and establishes a new identity when allocation is required.
Map/delete/clear and slice/copy mutate the shared reference value. Arrays retain
value-copy behavior. Nil slices/maps remain distinct from allocated empty values.

## Standing complex-number exclusion

`complex`, `real`, and `imag` are intentionally excluded from this slice. The
evidence is in the current scalar boundary: `syntax/bashpp_short.go` admits Go
`INT`, `FLOAT`, `CHAR`, and `STRING` basic literals but not `IMAG`, while
`interp/bashpp_collection.go`'s `bashPPScalarAny` can materialize strings,
booleans, integers, and floats but has no complex representation. Although
parameter validation can recognize a textual complex number with
`strconv.ParseComplex`, the single runtime value model cannot construct,
preserve, compare, or bind a complex scalar without flattening it to a string.
Adding those builtins before that representation exists would therefore claim
semantics the runtime cannot preserve. This is a standing ledger exclusion,
not an implicit omission.
