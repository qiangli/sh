# Go-source dependency sessions

Sprint: #118 · Story: #50 · Story-ID: cf81e4868348

This is a bounded implementation of imported dependency execution for the
Go-source interpreter. It does not certify complete Go compatibility.

The interpreter evaluates original program expressions, declarations, and local
function bodies. A persistent child executes exported imported functions and
methods through a fixed reflection protocol. Generated helper source contains
dependency imports, export/type registries and private connection constants. It
never contains the original program body, expression source, or oracle output.

`GoSourceModuleDir(originalModuleDirectory)` binds import resolution and helper
builds to the original module while `Dir(runtimeDirectory)` controls dependency
runtime cwd and assets. The selected SDK supplies export data and builds the
helper. SDK build environment overrides are separated from the program runtime
environment. The import policy runs before a dependency starts.

All dependency imports, including blank imports, initialize before original
package globals, init functions and main. Program arguments, stdin, stdout,
stderr and runtime environment are supplied when the child starts; transport
credentials do not appear in its arguments or environment. Named scalar types
retain package-qualified identities. Native objects use session-tagged handles;
no Go heap pointer crosses the process boundary. Handles live until the session
ends. Each Run closes its session; Reset cannot preserve dependency process
state. Cancellation kills the owned process group on Unix and removes generated
helper source/artifacts. Other platforms kill the helper process.

The targeted regression command is:

```
GOMAXPROCS=2 GOTOOLCHAIN=go1.27.0 go test -race -p=2 ./interp \
  -run '^TestGoSource(PersistentNativeBridge|Bridge)' -count=1 -v
```

The verified set contains 13 original-source cases compared across native Go,
Runner, and a compiled lowered artifact: persistent environment state, scalar
results, interpreter side-effect order, slices, maps/anonymous structs, named
scalars, native time/random handles and methods, nil errors, tuple arguments,
stdin/argv, dependency subprocess environment, and a blank constraint-package
import. Two private-module tests compare native Go with Runner for blank/named
imports, initialization order, original arguments during dependency init,
constraint-only interfaces, generic aliases, and separate source/runtime cwd.
Three lifecycle tests verify Reset environment isolation, public Subshell/Reset
ownership with independent parent and child dependency state, and cancellation
while a real dependency subprocess is active, including process termination and
cleanup.
These are scoped tests, not full corpus or cross-platform certification.

Remaining unsupported or uncertified semantics include interpreted callbacks
passed into dependencies, generic imported calls, mutation/alias preservation
when interpreted aggregates are copied into native arguments, complete local
named aggregate type identity, general native panic/recover interoperability,
and cloning live native state into a public Subshell. Native handles from a
different session fail closed. Constraint-only interfaces and generic type
aliases are omitted from runtime reflection registries; blank imports register
no exports. These limits require parent-story follow-up and must not be counted
as passing corpus coverage.
