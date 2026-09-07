# Agentic scope lowering groundwork

`lower/shellrt/agentic.go` carries the Bash++ assistance contract documented in
[`bashpp-agentic.md`](bashpp-agentic.md) into generated Go. It is the runtime
half only: an explicit immutable frame value, the callable entry check, and the
observation a cooperating in-process tool reads. The statement and expression
dispatcher in `lower/compile.go` does not thread frames yet, so this groundwork
alone does not certify compiled agentic behaviour; the emitter work follows in
the compiler-integration stories (03 and 06).

The contract is a source opt-in. The runtime therefore selects no model, opens
no connection, spends nothing, and grants no permission. There is no numeric
level, no provider abstraction, and no environment authority: `BASHY_AGENTIC`
does not supply the opt-in for the interpreter and supplies nothing here either.

## Why an explicit value

The interpreter keeps one `bashPPAgentic` field on the `Runner` and saves and
restores it around every region. Generated Go has no runner to hang that field
on, and reconstructing one would mean either goroutine identity, a thread-local,
or a mutable package-level scope. All three are rejected: a compiled program's
tasks, pipelines and subshells are ordinary goroutines, and an ambient scope
would be both racy and observable across regions that the contract says are
independent.

`Frame` is instead an immutable value threaded explicitly through generated
code. Every transition returns a new frame; no method mutates its receiver.
Restoration on return, failure, panic and cancellation then needs no bookkeeping
at all, because a callee never held a reference to its caller's frame. Copying a
frame into a goroutine is race-free for the same reason.

## API

```go
type Frame struct{ /* unexported */ }   // zero value: assistance off

func Off() Frame                        // explicit assistance-off frame
func Source(optIn bool) Frame           // a compiled unit's source opt-in
func Callback() Frame                   // trap or mapfile/readarray -C entry
func File() Frame                       // sourced script or new file run entry

func (f Frame) Agentic() bool           // the observation
func (f Frame) Block() Frame            // agentic { ... } entry
func (f Frame) Eval() Frame             // eval inherits its current frame
func (f Frame) Child() Frame            // subshell, pipeline element, task
func (f Frame) Defer() Frame            // capture at scheduling time
func (f Frame) Enter(site Site, marked bool) (Frame, error)

type Site struct{ Name, File string; Line int }
type PermissionError struct{ Site Site }

func (f Frame) Context(ctx context.Context) context.Context
func Agentic(ctx context.Context) bool
var Adapter func(ctx context.Context, agentic bool) context.Context
```

Each region rule maps to exactly one helper:

| Contract rule | Helper |
| --- | --- |
| Script opts in with an entry block | `Source(optIn)` |
| `agentic { ...; }` in the current shell | `Frame.Block` |
| Marked callable requires an agentic caller | `Frame.Enter(site, true)` |
| Ordinary helper or closure body runs with assistance off | `Frame.Enter(site, false)` |
| `eval` uses its current scope | `Frame.Eval` |
| Subshells, pipelines and tasks copy independently | `Frame.Child` |
| Deferred calls retain their scheduling scope | `Frame.Defer` |
| Trap and `mapfile -C` callbacks start off | `Callback` |
| Sourced scripts and new file runs start off | `File` |

`Block` opts in unconditionally, which is why it ignores its receiver: an
explicit block is the opt-in, never a widening of an inherited one. `Enter`
derives the body frame from the *declaration* rather than the caller, which is
what makes a plain helper called inside a block run with assistance off, and a
marked callable called from a block run with it on.

## Callable entry: the check precedes the body

`Enter` is the whole of the "requires an agentic caller" rule, and generated
code must call it before emitting any body statement. A denial returns the
error and an assistance-off frame; the body never runs, so a denied call makes
no assistance request at all. Ordering, not just outcome, is part of the
contract — the interpreter fails before the body executes, and a compiled run
that consulted a provider first and refused afterwards would already have
spent the request.

## Preserving public callable signatures

A frame cannot appear in a lowered callable's public Go signature: the emitted
input and output types of a Bash++ function or method are its contract with
ordinary Go callers. The frame therefore travels through compiler-private
plumbing:

1. Each callable emits a private entry that takes the caller's frame ahead of
   the declared parameters and holds the body.
2. The exported symbol keeps its exact declared signature and forwards to the
   private entry with a standalone entry frame. A marked callable invoked
   through the public symbol from outside the compiled program is consequently
   denied, which is the same answer the interpreter gives for a call outside an
   explicit block.
3. A call whose caller frame is statically known — every call the compiler
   emits — calls the private entry directly and passes its frame.

### Methods, interfaces and function handles need resolved metadata

The contract says a marked callable keeps its declaration when passed as a
value or through an interface: `f := marked` and `var i I = v` still reach a
marked body, and `interp.TestBashPPAgenticScopes` pins that. A bare public Go
func value cannot express this. It carries neither the marker nor the private
frame parameter, so a call through it would be indistinguishable from an
ordinary helper — the same reason `export -f` refuses marked functions, since
Bash's `BASH_FUNC_*` transport cannot retain the contract either.

Every indirect route — method value, interface dispatch, returned handle,
callback argument, alias chain — must therefore resolve to the callable's
private entry together with its declaration marker, and the resolution must be
available where the call happens. Two workable shapes, to be chosen in the
integration story:

- the compiler-private value used for handles is a pair of the private entry
  and the marker, and the public func value is produced only where a caller
  outside the compiled program needs one; or
- the check is emitted inside the private entry, and every indirect route is
  required to reach that entry, so no representation can bypass it.

What is not workable is inferring the marker from the static interface or func
type: for interface dispatch the marker belongs to the implementing method and
is only known from the dynamic value.

Whether a denied public wrapper with declared results should return zero values
plus status 1, or propagate a Go error, is an open integration question. The
groundwork settles the diagnostic and the status, not the result convention.

## Permission failure

`PermissionError` reproduces the engine's message byte for byte, and reporting
it through the existing `Fail` gives the engine's exit status 1:

```go
body, err := caller.Enter(shellrt.Site{Name: "f", File: origin, Line: line}, true)
if err != nil {
    shellrt.Fail(err) // Status = 1, message on Stderr
    return
}
```

The source position has exactly two spellings, matching the interpreter's two:

- **With a caller.** The caller supplies the file and its *call statement* line,
  producing `agentic.bpp: line 3: f: agentic action requires an explicit
  agentic { ...; } scope`. An empty `Site.File` falls back to `bash`, as the
  engine does for unnamed input. The line is the call statement's, including
  when the call is nested inside another function.
- **Standalone entry.** A callable reached with no in-program call site has no
  call statement to report, so it uses the engine's unprefixed form,
  `f: agentic action requires an explicit agentic { ...; } scope`. That is the
  wording `interp.New` produces by default, rather than a position spelling
  bash never emits.

`TestPermissionFailureMatchesEngine` runs the interpreter on equivalent source
for all four combinations and compares the produced bytes and status, so the
parity claim is measured rather than asserted.

## Handler-context adapter

`interp.HandlerContext` already exposes `Agentic` to exec middleware. Nothing
outside `interp` can construct or inject one: both `handlerCtxKey` and the
`runner` field are unexported. The minimal adapter therefore does not try, and
touches no `interp` or session file.

**Tools called directly from generated Go.** `Frame.Context` records the
observation under this package's own key, and `shellrt.Agentic(ctx)` reads it
with no wiring at all. An embedder whose tools read a different key sets
`Adapter` once at startup to derive a further context; a nil result leaves the
recorded context in place. `Adapter` is process-level wiring in the style of
`Stdout` and `Stderr`, not agentic state — the frame remains the only authority
for the boolean handed to it.

**Shell regions bridged through an `interp.Runner`.** The supported adapter is
to hand the engine the opt-in it already understands: when the frame is on, run
the bridged region as an explicit `agentic { ...; }` block under
`syntax.LangBashPP`. The engine then derives its own scope, and every exec
middleware observes `interp.HandlerCtx(ctx).Agentic` with the engine as the
authority — no new `interp` API, no runner field access, and no divergence to
keep in sync. The wrapper must keep a shell separator before the closing brace,
and the region must already be a complete, compiler-identified shell region;
because the compiler owns that identification, `shellrt` deliberately ships no
source-text wrapper of its own.

This adapter cannot mark a callable, and it does nothing for tools invoked
directly from Go without an interpreter run — that is the first path above. If a
later story needs a compiled frame to seed a `Runner` without a source wrapper,
the minimal `interp` addition would be a single exported knob, for example a
`RunnerOption` seeding the runner's agentic field. That is a proposal for the
integration stories, not part of this groundwork.

## Verification

`go test -race ./lower/shellrt/` covers:

- every transition in the table above, including that a derivation never
  mutates its receiver;
- body frames derived from the declaration for marked, plain and closure
  entries;
- the marker check preceding the body, with a deterministic request collector
  showing a denied call produces no request and adds none afterwards;
- the region walk — before, inside, nested, marked, helper, helper's own block,
  callback, eval, child, sourced, after, deferred — as an ordered request list
  matching the interpreter's observations in `interp.TestBashPPAgenticScopes`;
- engine parity for the denial bytes and status, in all four position forms;
- frames surviving panic, error return and cancellation, and a cancelled
  context still carrying its observation;
- copies used concurrently across goroutines under `-race`, leaving the parent
  frame untouched;
- deferred calls retaining their scheduling frame in both directions;
- the default context observation, the adapter seam, and nil handling.

## Limits

The frame is state, not behaviour: nothing here performs, queues, or authorises
an assistance request, and `Agentic` reporting true only means the source opted
in. Regions are threaded by the compiler, so an emitter that forgets a
transition produces a wrong observation that this package cannot detect —
same-source interpreted/compiled observation tests belong with the emitter
work. Trap and `mapfile -C` callbacks, `eval`, sourcing and task boundaries
exist here as entry helpers only; the compiled program has no traps, no `eval`
and no sourcing until their own lowering slices land.
