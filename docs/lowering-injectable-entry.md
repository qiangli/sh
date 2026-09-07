# Injectable compiled entry

`programEntrySource(body, mixedShell)` emits a hygienic private entry and the
ordinary process `main` adapter. `programEntrySourceNamed` emits the same entry
with an explicitly selected name. The compiler owns `Options.Entry` validation,
collision checks, `Result.Entry`, imports and dispatcher wiring. An omitted
public entry option does not reserve the source name `Execute`.

The entry signature is:

```go
func Execute(opts ...shellrt.SessionOption) (status int, err error)
```

An embedder supplies context through `WithContext`, streams through `WithStdio`,
and a backend through `WithShellFactory`. Each invocation constructs a new
Program and Session. Defaults and caller options are assembled per invocation;
there is no generated package-global context, status, stream or factory. Explicit
caller options override defaults. The entry returns its failure and status; the
process adapter prints a returned error once and exits with that status. A body
which already reported a nonfatal command failure can return a nonzero status
with no additional error, preserving one diagnostic.

Only mixed units install a default `shellexec.New(shellexec.BashPP())` factory.
Typed-only entries do not import the shell backend or interpreter. An explicitly
injected factory can use:

```go
shellrt.WithShellFactory(shellexec.New(
    shellexec.BashPP(),
    shellexec.RunnerOptions(interp.ExecHandler(handler)),
))
```

The compiler's shell-region seam must preserve the active assistance frame in
both the native context and the interpreter's source boundary. Passing
`Program.Context` alone does not initialize the backend's language scope; the
current region adapter also emits an explicit `agentic` region when appropriate.
Provider requests can then observe both native frame context and
`interp.HandlerCtx(ctx).Agentic`. Marked entry checks precede provider execution.

## Evidence and integration limits

Helper tests build a separate importable module containing the actual emitted
entry, compile a Go host once with the race detector, delete both generated
source and host source, then run the binary without shell tools on PATH. The
host injects the real shellexec backend with a deterministic in-process command
handler. Twelve concurrent instances verify exact argv, cwd, stdin, context,
stdout and stderr. Further cases verify no provider call after denial, status 7,
provider error identity, and cancellation reaching the handler's context.
A separate typed-only artifact verifies its dependency graph excludes interp
and shellexec, and that default main prints one guard diagnostic with status 2.

These tests exercise the generated entry helper and real backend injection.
Their native body explicitly models a marked entry and one delegated command;
they do not claim that whole-source compiler dispatch already accepts provider
scripts. Actual `Compile` acceptance follows the owner's entry and shell-region
integration. Existing source-level package globals also remain a separate
compiler state-isolation obligation: creating a new runtime Program does not
by itself make native Go package variables private to that invocation.
