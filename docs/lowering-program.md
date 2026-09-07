# The program runtime helper

`shellrt.Program` is the state a generated Bash++ program threads explicitly
through every region it emits. It is the piece that lets a compiled artifact
have a *process*: an entry, a status, output, a panic contract, tasks and
resources with an ordered shutdown — without a single package-level variable
holding any of it.

It lives in `lower/shellrt/program.go`, alongside the session, task, channel,
readonly and agentic-frame foundations it composes. Like the rest of `shellrt`
it imports only the standard library, so a typed-only artifact still links no
interpreter.

## What it replaces

`runtime.go` carries the original scalar-output helpers over package globals:
`Status`, `Stdout`, `Stderr`, `Fail`, `Echo`, `Printf`, `Exit`. Those are
process-wide, so two compiled units in one process share a status and a stdout,
a task and its owner race on `Status`, and `Exit` calls `os.Exit` from inside
the runtime — where no shutdown has run yet.

`Program` owns the same surface per program:

| global spelling | program spelling |
| --- | --- |
| `rt.Status` | `p.Status()` / `p.SetStatus(n)` |
| `rt.Fail(err)` | `p.Fail(err)` |
| `rt.Echo(...)`, `rt.Printf(...)` | `p.Echo(...)`, `p.Printf(...)` |
| `fmt.Print`/`fmt.Println` | `p.Print(...)`, `p.Println(...)` |
| `rt.Exit()` | `os.Exit(p.Status())` in the generated entry |
| `panicChain` / `pushPanic` / `popPanic` | `p.PushPanic`, `p.PopPanic`, `p.Recovered` |
| the emitted `panicBoundary` defer | `p.Run` |

The globals are left in place for the existing emitter; retiring them is the
compiler owner's change, not this one.

## The type

```go
type Program struct {
    Context  context.Context
    Session  *Session
    Channels *ChannelScope
    Readonly *ReadonlyState
    Frame    Frame
}
```

Everything a region needs is reachable from one value, and nothing is reachable
any other way. `Session` owns the status, the standard streams, persistent
shell state and the tasks launched from the program. `Channels` is the channel
ownership scope. `Readonly` is the guard state, which is `RWMutex`-safe and so
is shared with tasks by identity. `Frame` is the immutable agentic frame, and
`Context` is the context an in-process tool observes — always derived from the
frame in effect, so the observation and the region cannot disagree.

## Constructing and deriving

```go
func NewProgram(opts ...SessionOption) (*Program, error)
func (p *Program) Enter(site Site, marked bool) (*Program, error)
func (p *Program) Block() *Program
func (p *Program) Child(ctx context.Context, session *Session) *Program
```

`NewProgram` builds the session from ordinary `SessionOption`s and initializes
the channel scope and the readonly state. The shell backend arrives from
outside:

```go
p, err := shellrt.NewProgram(
    shellrt.WithDir(dir),
    shellrt.WithShellFactory(shellexec.New(shellexec.BashPP())),
)
```

`shellrt` never imports `shellexec`, so there is no import cycle and no
interpreter in an artifact that has no dynamic region. The generated `main`
supplies the factory only when the unit actually needs one.

Every derivation returns a new `*Program` and leaves the receiver alone, which
is what makes a frame an observation rather than saved-and-restored state:

- **`Enter`** is the callable entry check and the whole of the "requires an
  agentic caller" rule. The marker is checked *first*, before any session,
  channel, context or provider work, so a denied call does nothing at all. It
  returns `(nil, *PermissionError)`, whose message is the engine's byte for
  byte.
- **`Block`** is the explicit agentic block: assistance on, unconditionally.
- **`Enter` and `Block`** share the caller's *sequential* panic bookkeeping and
  its session, channel and readonly identities. Only the frame — and the
  context derived from it — differ.
- **`Child`** is the task entry. It **forks** the panic bookkeeping, because
  Go's panic state is per goroutine and a task's unwind is not its owner's, and
  it takes the child session's status and streams. It **retains** the channel
  scope and the readonly state by identity: a task shares its owner's channel
  authority and its owner's marks. Remapping the marks of a *cloned* object is
  an explicit compiler obligation; the runtime does not infer it.

## The generated entry

`Run` returns the failure and prints nothing. Reporting and exiting belong to
the generated program:

```go
func main() {
    p, err := shellrt.NewProgram(/* ... */)
    if err != nil { /* ... */ }
    if runErr := p.Run(func(p *shellrt.Program) {
        // the compiled body
    }); runErr != nil {
        p.Fail(runErr)       // exactly one report, after Run joined and closed
    }
    os.Exit(p.Status())      // the only os.Exit; shellrt never calls one
}
```

The ordering matters and is the point of splitting the two: `Fail` runs after
`Run` has already cancelled, joined and closed, so a diagnostic is never
written while tasks are still producing output, and `os.Exit` is never reached
with a shell EXIT trap still owed.

A public wrapper — a generated exported Go function called from ordinary Go —
has the same shape with a different last step: it constructs its own program,
calls `Run`, and panics the returned error to its Go caller. An internal
callable is never a wrapper: it takes the threaded `*Program` and returns
ordinary results.

## `Fail` reports; it does not unwind

```go
func (p *Program) Fail(err error)
```

`Fail` writes `err` to the program's standard error and sets the status from
`err`'s own `ExitStatus() int` when it has one — 2 for a readonly violation or
an unrecovered panic, whatever a typed projection failure declares — and 1
otherwise. Recognition is generic, by `errors.As` against
`interface{ ExitStatus() int }`, so a guard error defined in another package
needs no registration here and no edit when it lands.

`Fail` never panics and never abandons the body. A *guard* failure that must
abort the body raises its typed error as a panic instead: the body's defers
unwind normally on the way out and `Run` catches it. `ExitCode(err)` is the
same recognition exposed for a caller that needs the status without reporting.

## `Run`

```go
func (p *Program) Run(body func(*Program)) error
```

**Catches every abort.**

- `ChannelAbort` becomes its original error, so cancellation ranking still sees
  the real cause rather than a wrapper.
- A panicked error carrying `ExitStatus()` — a readonly violation, a typed
  projection failure — is returned unchanged, keeping its status. It goes to
  stderr through `Fail` and never to stdout.
- Anything else, including a native Go panic, becomes `*PanicError`: status 2,
  and an `Error()` that is the source panic contract's text. A compiled
  artifact reports `panic: runtime error: index out of range [3] with length 0`
  and exits 2, not a Go stack trace.

**Reports the panic chain the way the engine does.** `PushPanic` records a
raised panic and returns the value to hand to Go's `panic`; `PopPanic` drops
the newest, which is what recovering one does; `Recovered` completes a recover,
returning the payload the source binding observes and setting the status — 0
for a recovered panic, and the empty string at status 1 when nothing was
active, which is how a script tells those two apart. The direct-only rule is
Go's own; this runtime adds no policy on top of it. A panic raised while
another is unwinding names both:

```
panic: first
	panic: second
```

**Shuts down in a fixed order.** The group is cancelled first, so tasks blocked
on owned channel operations are released rather than waited on; tasks are then
joined; the session is closed, which runs the shell's EXIT trap and releases
the backend under a context that cancellation has not poisoned; and only then —
and only for the program that owns the scope — is channel authority revoked. A
task entry never revokes the shared scope: a sibling still running would lose
its channels underneath it.

Cancelling before the join applies to a body that succeeded as much as to one
that failed. That is the engine's own rule — `bashPPWait` cancels at the file
boundary because "successful blocked tasks must not keep a File Run alive
forever" — so a compiled artifact terminates where the interpreted script
does.

**Ranks failures.** A genuine failure outranks a cancellation-class one
(`context.Canceled`, `context.DeadlineExceeded`, `ErrChannelScopeClosed`,
`ErrSessionClosed`). When a task's failure cancelled a receive the body was
blocked on, the reported error is the task's, not the cancellation the body
happened to observe. A tie keeps the body's own failure, and a backend close
failure is reported only when nothing else failed.

**Does not report the cancellation it caused itself.** This is the correction
to the first revision. For

```
func blocked(ch) { ch <- 1; }
func main() { ch := make(chan int); go blocked(ch) }
main()
```

the interpreter ends silently at status 0: `bashPPGo`'s task records no failure
at all when it ends cancelled. The first revision reported the shutdown's own
cancellation, so the artifact exited 1 with `shellrt: task 0: context canceled`
— a diagnostic the script never produces. `Run` now drops a cancellation-class
task failure when the program's own context was never cancelled from outside.
A sibling's genuine failure is unaffected (it outranks a cancellation and is
what `Join` returns anyway), and an externally cancelled or expired program
still reports, because that cancellation is the reason it stopped.

## Output

`Echo`, `Printf`, `Print` and `Println` write to the program's stream. `Printf`
implements the same `%s`, `%d`, `%%` and escape subset as the global helper,
including format recycling, omitted arguments and `\c`.

The status they leave is the engine's, and the engine was asked rather than
guessed at. Running `echo a` under `interp` with a writer whose write fails
leaves the script at status 0 with nothing on standard error: **a lost write is
not a failed command**. So `Echo` and friends set status 0 whether or not the
write landed, and return the write error for the caller to do with as it sees
fit. A *format* failure is a different thing and the engine does report it —
`printf '%d\n' abc` prints `printf: abc: invalid number` and ends at status 1 —
so an unreadable conversion argument sets status 1 after writing what it could,
and an unsupported conversion sets status 1 and writes nothing.

An earlier revision of this helper made a failed write sticky, so that a later
successful write could not reset the status to 0. That rule has no counterpart
in the source language — the engine records nothing for the failed write in the
first place — and it is gone. `lower/shellrt/program_test.go` now runs the
interpreter as the oracle for both cases rather than asserting an intuition.

> For the emitter: `words.go` currently emits
> `if err := rt.Echo(...); err != nil { rt.Fail(err) }`. On the `Program` path
> that would report and exit 1 where the interpreted script is silent at 0.
> A write error from `Echo` is information, not a command failure.

## What is not here

- No `os.Exit` anywhere in `shellrt`. A program that wants to end says so at
  its entry.
- No goroutine identity, thread-local, or ambient program lookup. A task gets
  its state because `Child` handed it over.
- No policy of its own on top of the engine's: the panic, recover and denial
  contracts are the ones `interp/bashpp_panic_test.go` and
  `docs/bashpp-agentic.md` already pin, reproduced rather than reinterpreted.

## Tests

`lower/shellrt/program_test.go` covers independent concurrent programs (under
`-race`), a task failure outranking the cancellation it caused in the body, a
task left blocked at shutdown ending as a silent success, an external
cancellation still being reported, a sibling failure not being mistaken for
one, the ordered and bounded shutdown against a stub backend, a task entry
leaving the shared channel scope alone, readonly status 2, marked-callable
denial at status 1 with no body, shared sequential and forked child panic
bookkeeping, recovery status, and the printf subset. Two tests run `interp`
itself as the oracle for the output status. A real built artifact exercises the
generated entry shape end to end — status, panic, nested panic, native panic,
readonly, denial, task failure and the blocked-task shape above — with no Go on
`PATH`.

## Handoff to the compiler owner

The API above is the signed one; `.agents/handoff-program.md` carries the same
contract in the local workspace (that directory is git-ignored, so this section
is the copy that travels with the change). Two decisions are flagged rather
than assumed:

1. **`Fail` is report-only.** It sets the status and writes the diagnostic; it
   does not abort the enclosing body and does not panic. A guard execution
   failure that must abandon the body therefore needs a distinct spelling —
   today that is a panic carrying the typed guard error
   (`panic(&shellrt.ReadonlyError{...})`, or the channel helpers'
   `MustChannelOperation`), which `Run` catches and returns with its status
   intact. If the emitter would rather say `p.Must(err)` or `p.Abort(err)` than
   open-code the panic at every guard site, that belongs in `program.go` and
   will be added there.
2. **`Print` and `Println` return nothing**, matching the signed signatures,
   while `Echo` and `Printf` return an error. Since a failed write is not a
   command failure, nothing observable is lost by the difference; say so if the
   emitter wants `error` results from all four instead.
3. **A write error must not be `Fail`ed** on this path, for the reason the
   Output section gives. If the emitter wants the strict-I/O behaviour anyway,
   that is a language decision to make deliberately, not one to inherit from
   `words.go`'s current `rt.Echo` spelling.
