# Native execution ABI and value boundaries

Units containing task/channel operations or agentic declarations use private
native implementations with explicit `*shellrt.Program` and `shellrt.Site`
parameters. Ordinary public Go wrappers keep their source signatures. In-unit
calls, captured function handles, closures and deferred calls target the private
ABI, so assistance comes from the invocation or scheduling context. It does not
come from a package global or the frame in which a handle was created.

Callable entry checks the resolved declaration's marker before running its
body. Ordinary callees receive assistance off. Explicit agentic blocks derive
an immutable frame while retaining the enclosing lexical binding scope. Typed
calls currently use the interpreter's unprefixed denial spelling; the source
map continues to retain their positioned syntax.

The generated process entry creates one Program, runs the native entry body,
then reports a returned error once and exits with the session's final status.
Private code uses the Program's status, streams, panic bookkeeping and task
session. Task creation captures the callee and arguments before launching,
then invokes against a child Program. Native channels use an explicit shared
ownership scope and cancellation-aware operations. Legacy untyped channel
parameters derive a native signature from committed make-channel call sites;
incompatible inferred signatures are diagnostics.

Pure native scalar units retain their existing standard-library-only output.
Runtime units import the explicit support package; they retain ordinary native
Go functions, variables and control flow. They contain no whole-source or
whole-AST interpreter wrapper. The compiler checks runtime imports against an
embedded snapshot of the support package's actual declarations, without
executing their bodies or relying on an installed source directory.

# Projection provenance

Binding metadata distinguishes native scalar, exact literal float, native
aggregate, imported object, pointer and interface roots. Shell interpolation
uses the corresponding projection: exact rational literal spelling, JSON
objects and nil collections, and the interpreter's pointer/interface rules.
Typed print is a distinct boundary: imported values keep their native shape,
while native aggregates take the interpreter's collection representation.

The compiler does not preserve a source literal after a possible write. Mutable
exact-float and imported-object provenance currently reports a positioned
unsupported diagnostic pending runtime provenance storage. This conservative
boundary also applies to writes in other functions; a compile-time walk cannot
pretend that visiting a function body executes its assignments.

# Current limits and verification

The private ABI currently covers direct functions, captured handles, closures,
defer, channels, task launch, select and channel range. Private/public adapters
for method/interface capabilities, returned callable signatures and foreign
function values remain explicit integration work. General dynamic shell
regions, readonly dispatch and checked native operation guards also retain
their separate runtime obligations.

Tests compile real native artifacts, remove their source, execute with shell
tools absent, and compare stdout, stderr and status with interpretation of the
same source. Covered combinations include allowed and denied marked calls,
ordinary helper assistance reset, marked handles, deferred scheduling frames,
task closures, channel handoff and scope ownership, exact literal floats,
imported string roots, aggregate shell versus typed-print formatting, tuple
return-call forwarding and live nil guards. These tests establish a delivery
slice; they do not replace the complete public acceptance inventory.

The current Program dependency still reports cancellation from its own normal
shutdown as status 1 for an unfinished blocked task, while interpretation exits
0. The artifact test retains this failure. Correcting that runtime behavior is
required before this slice can receive a combined acceptance gate.
