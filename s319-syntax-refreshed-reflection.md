# Refreshed reflection arguments

Sprint: #319; Story: #1087; Story-ID: 96cd7f0c400d

The fresh-binary TestDump failure occurs while printing SliceExpr.Index, an
array of Expr interfaces. Reproduce through interpreted Parse then Fdump with
`package p; func f() { h(text[1:]) }`. This reaches the same unmaterialized
original BasicLit handle as the upstream parser.go AST, without a large graph.

The runner materializes call arguments before session dispatch. Session slice
preparation then refreshes the variadic slice from interpreter storage and
restores host-only adopted handles. Materialize each refreshed snapshot before
it replaces the transport argument. Preserve the transport's final rejection
of descriptors that bypass preparation, and preserve the interpreter's cells.

Validation: the Parse/Fdump regression, existing Parse/Fdump and throughput
guards, fail-closed descriptor guard, reflected-method/lazy-ValueOf tests, and
the exact upstream TestDump through the harness-patched runner. Exact package
acceptance and Linux integration remain separate from the focused regression.
