# Sprint 165 interp-collections findings

Lane: S165.1 collection map keys, builtin blank targets and delete. Barrier C
contains 17 `unsupported map key type` rows plus two collection-index rows,
not 20 map-key rows. The index rows have a different first cause.

| root | first cause | mechanism | status |
|---|---|---|---|
| `testdir:abi/map.go` | pointer keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:fixedbugs/issue17752.go` | array keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:fixedbugs/issue22605.go` | array keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:fixedbugs/issue23734.go` | interface keys were excluded, including Go's run-time panic for an unhashable dynamic value | typed interface keys and recoverable unhashable-key panic | fixed by the comparable-map-key commit; leaf pending |
| `testdir:fixedbugs/issue29013b.go` | a defined integer key was not resolved through its underlying comparable type | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:fixedbugs/issue37716.go` | a generic defined key was not resolved through its instantiated underlying comparable type | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:fixedbugs/issue59411.go` | float keys, including distinct non-reflexive NaN entries, could not be represented by string-keyed storage | comparable typed map keys | direct map-key cause fixed; `reflect.Value.Clear` may expose a bridge cause on leaf |
| `testdir:fixedbugs/issue65957.go` | interface keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:fixedbugs/issue70189.go` | float keys, including non-reflexive NaN identity, could not be represented by string-keyed storage | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:ken/cplx5.go` | complex keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:maplinear.go` | float keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:nil.go` | float32 keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:struct0.go` | interface keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:typeparam/issue42758.go` | interface keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:typeparam/issue46591.go` | interface keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:typeparam/issue48453.go` | instantiated pointer keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:typeparam/metrics.go` | instantiated struct keys were excluded from the evaluator's map-key model | comparable typed map keys | fixed by the comparable-map-key commit; leaf pending |
| `testdir:closure2.go` | an immediately invoked function literal in an array index is rejected before collection lookup | scalar call expression in index position | moved to the expression-conversion lane |
| `testdir:typeparam/issue47723.go` | a nested immediately invoked function literal in an array index is rejected before collection lookup | scalar call expression in index position | moved to the expression-conversion lane |

## Requests to other seams

- Expression-conversion owner: evaluate immediately invoked function literals
  in collection index position for `closure2.go` and
  `typeparam/issue47723.go`; no map-key change is involved.
