# Native declaration continuation

An embedding host can reuse native shell declarations across independently
compiled entries with one explicit continuation:

```go
continuation := shellrt.NewNativeContinuation()
options := []shellrt.SessionOption{
    shellrt.WithNativeContinuation(continuation),
    shellrt.WithShellFactory(sharedBackendLease),
}
code, err := first.Execute(options...)
// The host calls its actual interpreter runner's Reset here.
code, err = second.Execute(options...)
```

The host owns the backend and its leases, including final shutdown. The
continuation retains native declarations and their captured lexical cells.
Each entry still creates fresh root bindings, session state, permissions and
channel scope. A retained function uses the invocation's permission frame;
retained channel values cannot acquire authority in the new scope. Native
entry execution through one continuation is serialized, and a waiting entry
honors context cancellation. Backend construction and lease management remain
the host's responsibility.

The acceptance test compiles two separate packages, removes generated sources,
and runs a race-built host against one real backend and an actual Runner.Reset.
The retained function sees `x=1` before and after Reset; the second entry's root
independently sees `x=9`. Typed declarations never enter the backend.
