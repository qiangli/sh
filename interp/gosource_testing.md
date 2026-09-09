# Interpreted Go test callbacks

Sprint: #118; Story: #56; Story-ID: 3ef468f4e831

`Runner.LoadGoSourceTests(ctx, program)` resets the runner and loads a non-main
package produced by `gosource.Load(sources, Options{RunMain: false, ...})`.
The caller supplies all applicable original companion files, authenticates their
bytes and records their source identities. Global initialization and original
`init` functions execute once through the interpreter. The session retains its
native dependency bridge until `Close` or `Runner.Reset`.

A dedicated native harness owns `testing.Main` and creates `testing.InternalTest`
callbacks whose entire generated body is:

```go
func(t *testing.T) {
    if err := session.Run(ctx, originalFunctionName, t); err != nil {
        t.Fatal(err)
    }
}
```

The original test package is never imported into that native harness. Its
functions are loaded ASTs, invoked by the interpreter. The real `*testing.T`
capability stays in the same process and on the native scheduler's callback
goroutine. Ordinary imported dependencies continue to use the existing bridge;
the dependency bridge must not compile or execute original test bodies.

The first adapter accepts original `func(*testing.T)` functions and implements
scalar `Error[f]`, `Log[f]`, `Fatal[f]`, `Skip[f]`, `Fail`, `FailNow`, and `SkipNow`
calls. Helper functions can receive the capability. Deferred receiver and
argument values are captured when the defer is registered. A private control
unwind runs every interpreted defer before invoking the real scheduler's
`FailNow`/`SkipNow`; it cannot be caught by interpreted `recover`.

A session and its runner are serial resources. Callbacks may reenter after the
previous callback returns or exits through the testing scheduler. Ordinary
`Runner.Run` cannot replace an active session; Reset invalidates old sessions.
Unsupported testing methods fail explicitly. This is not a general implementation
of `testing.T`: subtests, Parallel, Cleanup, Helper attribution, benchmarks,
examples, fuzzing, TestMain, composite formatting values and callbacks crossing
the native dependency-process boundary remain pending. Callers must turn every
returned interpreter error into a native test failure.

The pinned integration proof loads all four unmodified Go 1.27 `errors_test`
companions and invokes `TestNewEqual` and `TestErrorMethod`. Its source hashes are
checked before loading and bytes compared after execution. It is a two-function
slice, not all-stdlib product coverage. The corpus inventory and its full package,
source and dynamic-test denominators remain separate acceptance obligations.

Focused scheduler and lifecycle verification:

```sh
go test ./interp -run '^TestGoSourceTestingControlAndLifecycle$' -count=1
```

Pinned source integration (requires the authenticated Go 1.27 SDK as GOROOT and
toolchain, plus the frontend and dependency-bridge implementation):

```sh
go test ./interp -run '^TestGoSourceTestingOriginalErrors$' -count=1
```

The controls use separately authored adapter fixtures, fake capabilities for
precise log/defer assertions, and a child of the real test executable to verify
that an actual FailNow fails only its subtest while the following callback runs.
They also exercise actual SkipNow, cancellation, unsupported methods, initialization
and Reset invalidation. Passing them does not substitute for the pinned original
source integration proof.

Initial measured boundary: on the base product, the pinned proof fails while
converting `wrap_test.go:425` (`[]error(m)`). With the committed frontend
`117dde41` prerequisite temporarily applied, all four files convert but package
loading fails at `example_test.go:16:1`: imported `time.Time` is not recognized
as a struct field type. Neither selected original test body has completed.
The integration test deliberately retains this failure; it must pass before
claiming the original-source slice or merging it as completed acceptance.
Further imported-handle comparison and method obligations can emerge after
package loading works. Scheduler-control checks passing are separate evidence.
