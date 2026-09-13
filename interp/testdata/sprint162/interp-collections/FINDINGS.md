# Sprint 162 interp-collections findings

| root | first cause | mechanism | status |
|---|---|---|---|
| `testdir:fixedbugs/bug047.go` and collection element family | untyped numeric constants rendered as rational strings before contextual element assignment | destination-driven scalar conversion for collection elements | fixed in pending commit |
| `testdir:clear.go`, `testdir:fixedbugs/issue43619.go`, `testdir:typeparam/maps.go`, `testdir:typeparam/mapsimp.go`, `testdir:typeparam/sliceimp.go`, `testdir:typeparam/slices.go`, `testdir:zerodivide.go` | non-finite dependency scalar results retained their bridge transport wrapper when assigned into typed collection elements | bridge scalar materialisation at the collection destination | fixed in pending commit |
| `testdir:abi/fuzz_trailing_zero_field.go`, `testdir:cmplxdivide.go`, `testdir:fixedbugs/bug329.go`, `testdir:fixedbugs/bug466.go`, `testdir:typeparam/absdiff2.go`, `testdir:typeparam/absdiffimp2.go` | finite complex values use a lossless string carrier, but typed collection validation rejected that carrier | destination-validated complex collection carrier | fixed in pending commit |
| `testdir:fixedbugs/bug272.go`, `testdir:fixedbugs/issue15252.go`, `testdir:fixedbugs/issue19799.go`, `testdir:fixedbugs/issue22083.go`, `testdir:fixedbugs/issue27289.go`, `testdir:fixedbugs/issue29504.go`, `testdir:fixedbugs/issue30116.go`, `testdir:fixedbugs/issue4353.go`, `testdir:fixedbugs/issue71759.go`, `testdir:fixedbugs/issue75327.go`, `testdir:fixedbugs/issue79197.go`, `testdir:fixedbugs/issue79236.go`, `testdir:fixedbugs/issue79236b.go`, `testdir:fixedbugs/walk_bounded_overshift_empty_bound.go`, `testdir:recover2.go` | dynamic sequence bounds faults were returned as evaluator diagnostics instead of entering Go panic unwinding | recoverable runtime index panic | fixed in pending commit; roots asserting the concrete runtime error type still need leaf review |
| `testdir:fixedbugs/issue14591.go` | the reported index is in bounds for the source array and is therefore a secondary symptom, not a runtime bounds fault | pointer/returned-array representation | moved to `interp-nilptr` |
| `testdir:ken/array.go`, `testdir:ken/simparray.go` | indexed assignment through `*[N]T` inspected pointer metadata as collection metadata without Go's implicit array dereference | pointer-to-array indexed assignment | fixed in pending commit |
| `testdir:fixedbugs/bug059.go`, `testdir:ken/string.go` | nested map/slice and direct array assignment pass the outside-corpus controls on the merged baseline | existing collection assignment mechanisms | needs leaf re-measure; no new mechanism justified |
| `testdir:fixedbugs/issue32477.go` | the failing indexed assignment is reached through an intentional nil pointer fault | nil pointer runtime panic | moved to `interp-nilptr` |
| `testdir:235.go`, `testdir:chan/zerosize.go`, `testdir:fixedbugs/bug285.go` | value-position `make` only dispatched raw channels with dependency-owned element types; defined channels and aggregate element channels fell through to slice/map validation | unified Go channel allocation for value builtins | fixed in pending commit |
| `testdir:typeparam/append.go` | `make` receives an instantiated named slice type, whose underlying collection shape is already supported on the merged baseline | named collection type resolution | needs leaf re-measure; no new mechanism justified |
| `testdir:fixedbugs/issue68816.go`, `testdir:fixedbugs/issue80517_1.go`, `testdir:fixedbugs/issue80517_2.go`, `testdir:fixedbugs/issue80517_3.go` | dynamic negative slice lengths returned a static evaluator diagnostic instead of Go's recoverable runtime panic | runtime `makeslice` size panic | fixed in pending commit |

## Requests to other seams

None.
