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

## Structured native values (Sprint #118, Story #52, Story-ID d564bada90bb)

An imported call or package variable that is not a scalar answers with a
session handle. The original program's `len`, `cap`, index, slice, field and
`range` expressions read through that handle, so the dependency keeps ownership,
identity and mutation of the value and no interpreter copy of the sequence is
materialised. Only the access is forwarded; the surrounding expression,
statement and loop bodies stay on the interpreter path. A scalar element binds
as an ordinary typed interpreter variable, a non-scalar element keeps its
handle, and Go's own index/slice bounds errors are reported by the dependency.
`os.Args[3]`, `os.Args[1:]`, `u.Host` and `for _, e := range os.Environ()` are
the covered shapes. The three-index slice form is refused rather than
reinterpreted, because its capacity operand has no interpreter meaning.

A dependency process that ends on its own — `os.Exit`, or a successful
`syscall.Exec` replacement — is now reported as the original program's exit
status instead of a bridge failure. A non-zero status becomes the interpreter's
status and suppresses later bridge diagnostics on the same unwind; a zero status
ends the run with no error. Deferred interpreter work does not run, matching Go.
The session is closed at that point, so a later `Run` starts a fresh dependency.

Handles store an addressable copy, so pointer-receiver methods of a value-typed
native remain reachable and keep mutating the value behind the same handle.

`TestGoSourceNativeStructuredValues`, `TestGoSourceNativeSelfTermination` and
`TestGoSourceNativeSessionLifecycle` compare unchanged original sources against
a real Go build of the same file on stdout, stderr and exit status.

Still unsupported after this slice, and not counted as coverage: declaring a
variable of an imported non-scalar type (`var ops atomic.Uint64`,
`var mu sync.Mutex`, `embed.FS`), which needs the shared declaration and type
registry paths; carrying a handle through an interpreted function's parameters
and results (`defer closeFile(f)`); taking the address of an interpreter
variable for a native out-parameter (`flag.IntVar(&n, ...)`); interpreted
callbacks passed into dependencies (`wg.Go(func(){...})`); and indexing a plain
interpreter string. The generated helper workspace is also still visible in the
runtime directory while the program runs, so a program that lists its own cwd
does not match Go byte for byte.

Remaining unsupported or uncertified semantics include interpreted callbacks
passed into dependencies, generic imported calls, mutation/alias preservation
when interpreted aggregates are copied into native arguments, complete local
named aggregate type identity, general native panic/recover interoperability,
and cloning live native state into a public Subshell. Native handles from a
different session fail closed. Constraint-only interfaces and generic type
aliases are omitted from runtime reflection registries; blank imports register
no exports. These limits require parent-story follow-up and must not be counted
as passing corpus coverage.
