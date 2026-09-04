# Bash++ P3-D — panic, recover, and panic-safe unwinding

Sprint 114 · story `b2c7e6409da1` · 2026-09-03

P3-D adds Go's `panic` and `recover` to the committed sequential subset, and
makes the frame machinery they run through safe against every way a frame can
leave. `panic` and `recover` are predeclared, not keywords: a session that
declares `func panic(...)` shadows the predeclared one, and a script that
defines a shell function named `panic` or `recover` is untouched.

## What a panic is

A panic is a control transfer, not a failed command. It abandons the current
statement, and every frame between it and its handler, running each abandoned
frame's deferred calls in LIFO order as it goes. Execution never resumes at the
panic site, in the caller, or anywhere else the frame would have reached.

It is modelled as runner state (`bashPPPanicState`) rather than an exit status,
because a status can express neither of the two things that matter: that the
statements after the panic site must not run, and that the cleanups must. The
unwind reuses the halt the interpreter already has — `Runner.stop` reports a
live panic — so the halt holds across every boundary that consumes a `return`,
including a shell function frame. There is no host-level Go panic anywhere in
the implementation.

## Recover

`recover` succeeds only where Go says it does: in a call the unwinding frame
deferred *directly*. The implementation is one comparison — the call stack must
be exactly one frame deeper than the frame whose defers are running — and it
covers every case the spec lists. A recover in the panicking body is too
shallow; one in a function called by the deferred call is too deep; and
`defer recover()` never pushes a frame at all, so it does not recover, as Go
documents.

`recover` reports twice, because the payload alone cannot answer the question Go
answers with nil: a panic value may itself be the empty string. It yields the
payload as its single result and sets status 0 when it recovered, status 1 when
there was nothing to recover. `err := recover()` is the spelling; a bare
`recover()` statement recovers just as well, discarding the payload.

## Results and defers

Results are settled *before* a frame's deferred calls run, as Go sets result
parameters before running defers, and a named result is written into its
binding at that point and read back afterwards. That ordering is what lets a
deferred call amend a returned value, and it is the only way a recovered frame
can produce one at all: an abandoned frame's named results are whatever the
recovering defer left them, and its unnamed results are zero values, since no
deferred call can reach a result with no name.

## Where a panic stops — the termination policy

1. **A deferred call recovers it.** The frame whose defers are running returns
   normally and the caller cannot tell that panic happened. A panic raised
   while another is unwinding becomes the active panic. Recovering the newer
   panic inside that deferred call lets the older panic continue unwinding;
   a later defer may recover the older panic in turn.
2. **It escapes the outermost Go-form frame.** Nothing further can recover it,
   so it is reported and the shell terminates with status 2, as an unrecovered
   Go panic exits 2. The report is the panic chain — `panic: first` followed by
   a tab-indented `panic: second` for each panic that replaced it — and nothing
   else: a fabricated Go stack trace would name frames this shell does not have.
3. **A deferred call runs an explicit `exit`.** The script asked to terminate.
   It terminates with that status, the remaining cleanups do not run, and the
   panic is neither reported nor propagated — the precedence `os.Exit` has over
   a panic in flight.

A subshell is a process boundary and a panic never crosses one. A subshell
carries in neither an active panic nor a Go-form frame, so a panic raised inside
one settles inside it, giving that subshell status 2, which the parent observes
like any other status.

## Frame restoration

Entering and leaving a Go-form frame are now one decision each (`bashPPFrame`),
and leaving happens through a Go `defer` in the invoker. Positional parameters,
the write environment, the typed scope, the call stack, the deferred-call stack,
the in-function flag and the pending return are restored on every exit path,
including the ones that do not reach the end of the invoker. The stacks are
truncated back to the depth the frame began at rather than popped by count, so
a frame abandoned mid-unwind can neither leave a deeper stack behind nor pop one
entry too many.

## Parser surface

The only shape the parser had to claim is the zero-argument `recover()`, which
is also the prefix of a classic shell function definition and so is recognized
only for a name already known to be callable. `panic` and `recover` join that
set as predeclared names under `LangBashPP` alone. The claim is bounded on both
sides: `recover()` followed by a statement end is Class R — bash rejects it
today — while `recover() { … }` still rewinds to the shell function definition
it has always been.

## Deliberately excluded

Sprint 115 owns concurrency and typed process boundaries. Nothing here crosses
a goroutine or a process: there is no panic propagation out of a background
job, no cross-subshell handler, and no channel or cancellation interaction.
Struct, interface and generic values remain Sprint 116's, so a panic payload is
the shell's string value, not an arbitrary typed one.
