# Sprint 162 interp-collections findings

| root | first cause | mechanism | status |
|---|---|---|---|
| `testdir:fixedbugs/bug047.go` and collection element family | untyped numeric constants rendered as rational strings before contextual element assignment | destination-driven scalar conversion for collection elements | fixed in pending commit |
| `testdir:clear.go`, `testdir:fixedbugs/issue43619.go`, `testdir:typeparam/maps.go`, `testdir:typeparam/mapsimp.go`, `testdir:typeparam/sliceimp.go`, `testdir:typeparam/slices.go`, `testdir:zerodivide.go` | non-finite dependency scalar results retained their bridge transport wrapper when assigned into typed collection elements | bridge scalar materialisation at the collection destination | fixed in pending commit |
| `testdir:abi/fuzz_trailing_zero_field.go`, `testdir:cmplxdivide.go`, `testdir:fixedbugs/bug329.go`, `testdir:fixedbugs/bug466.go`, `testdir:typeparam/absdiff2.go`, `testdir:typeparam/absdiffimp2.go` | finite complex values use a lossless string carrier, but typed collection validation rejected that carrier | destination-validated complex collection carrier | fixed in pending commit |
| `testdir:fixedbugs/bug272.go`, `testdir:fixedbugs/issue15252.go`, `testdir:fixedbugs/issue19799.go`, `testdir:fixedbugs/issue22083.go`, `testdir:fixedbugs/issue27289.go`, `testdir:fixedbugs/issue29504.go`, `testdir:fixedbugs/issue30116.go`, `testdir:fixedbugs/issue4353.go`, `testdir:fixedbugs/issue71759.go`, `testdir:fixedbugs/issue75327.go`, `testdir:fixedbugs/issue79197.go`, `testdir:fixedbugs/issue79236.go`, `testdir:fixedbugs/issue79236b.go`, `testdir:fixedbugs/walk_bounded_overshift_empty_bound.go`, `testdir:recover2.go` | dynamic sequence bounds faults were returned as evaluator diagnostics instead of entering Go panic unwinding | recoverable runtime index panic | fixed in pending commit; roots asserting the concrete runtime error type still need leaf review |
| `testdir:fixedbugs/issue14591.go` | the reported index is in bounds for the source array and is therefore a secondary symptom, not a runtime bounds fault | pointer/returned-array representation | moved to `interp-nilptr` |

## Requests to other seams

None.
