# Sprint 162 interp-collections findings

| root | first cause | mechanism | status |
|---|---|---|---|
| `testdir:fixedbugs/bug047.go` and collection element family | untyped numeric constants rendered as rational strings before contextual element assignment | destination-driven scalar conversion for collection elements | fixed in pending commit |
| `testdir:clear.go`, `testdir:fixedbugs/issue43619.go`, `testdir:typeparam/maps.go`, `testdir:typeparam/mapsimp.go`, `testdir:typeparam/sliceimp.go`, `testdir:typeparam/slices.go`, `testdir:zerodivide.go` | non-finite dependency scalar results retained their bridge transport wrapper when assigned into typed collection elements | bridge scalar materialisation at the collection destination | fixed in pending commit |
| `testdir:abi/fuzz_trailing_zero_field.go`, `testdir:cmplxdivide.go`, `testdir:fixedbugs/bug329.go`, `testdir:fixedbugs/bug466.go`, `testdir:typeparam/absdiff2.go`, `testdir:typeparam/absdiffimp2.go` | finite complex values use a lossless string carrier, but typed collection validation rejected that carrier | destination-validated complex collection carrier | fixed in pending commit |

## Requests to other seams

None.
