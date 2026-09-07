# Native task policy at the shell boundary

A generated task belongs to `shellrt.Session.Go`, while a dynamic shell region
runs in an interpreter backend. These two ownership layers must agree on task
I/O restrictions even though the backend did not launch the native task.

`Session.Go` marks its body and child-session contexts. `shellrt.InTask` lets a
backend observe that marker; derived contexts and cancellation-free cleanup
retain it. The standard backend configured with `BashPP()` maps it to
`interp.WithTaskPolicy` during both shell execution and EXIT-trap cleanup.
Backends configured for ordinary Bash retain ordinary Bash task behavior.

`WithTaskPolicy` scopes the existing interpreter task restrictions to that Run.
For example, mapfile refuses non-regular input before constructing a scanner,
read uses its cancellable task input path, and process signal operations remain
restricted. Ordinary runs without the context policy keep ordinary shell
behavior. No command-name detection in the compiler is required.

The context policy does not claim interpreter task ownership. Source tasks
launched inside a backend still use the interpreter's own task registry and
normal EOF join/cleanup. Setting the private source-task ownership flag instead
would suppress that cleanup and is deliberately avoided.

This policy supplements the existing blocking observer: the observer announces
actual suspension to the native launcher; the policy selects operations that
can safely run in the task. It does not make arbitrary injected handlers
cancellable. No callable, agentic or channel authority is encoded by the marker.
