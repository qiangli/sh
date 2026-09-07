# Task admission through dynamic shell regions

A task retains its launch until it completes or reaches a blocking operation.
An empty descendant join and an immediate shell builtin do not release the
launcher. This lets an immediate failure prevent later tasks from starting.

`shellrt.BlockingShellRunner` is an optional extension of `ShellRunner`.
Its `RunShellWithBlocking` and `CloseWithBlocking` methods receive a callback
which the backend invokes before a blocking operation. The production
`shellexec` backend supplies that callback through
`interp.WithBlockingObserver(ctx, callback)`, including while running EXIT
traps. Existing interpreter suspension sites notify for operations such as
external commands, waiting reads and waits; parsing and immediate builtins do
not notify. Observer state belongs to the context and crosses no package-global
or thread-local boundary.

Backends implementing only `ShellRunner` retain their conservative contract:
entering their execution or cleanup method releases the launcher. An embedder
which needs precise task admission should implement the optional interface.
Custom handlers remain responsible for honoring cancellation and for exposing
any blocking work outside the interpreter's existing suspension sites. This
observer does not make arbitrary user I/O or handlers cancellable.

Acceptance exercises compiled importable entries with the source files removed,
independent concurrent invocations, and the race detector at GOMAXPROCS 1, 2 and
4. Runtime checks also cover immediate failure, a blocking provider, blocking
EXIT-trap cleanup, and the legacy backend contract.
