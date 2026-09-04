// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Go-compatible `panic` and `recover` for the sequential Bash++ subset.
//
// A PANIC IS NOT AN EXIT STATUS. The distinction this file exists to keep is
// that a panic is a *control transfer*, not a failed command: the statements
// after the panic site never run, each active frame is abandoned rather than
// returned from, and every frame's deferred calls still run as it is abandoned.
// A non-zero status would give none of that, which is why the panic lives in
// its own runner state instead of being encoded into [exitStatus].
//
// HOW IT UNWINDS. There is no host-level Go panic here. Unwinding reuses the
// halt the interpreter already has: [Runner.stop] reports a live panic, so no
// further statement runs anywhere, at any depth, no matter which boundary
// cleared the `returning` flag on the way out. The flag is still set alongside
// it, because that is what makes a loop *break* rather than spin through
// no-op iterations. Every frame boundary then asks one question — am I still
// panicking? — and either keeps unwinding or resumes normally.
//
// WHERE IT STOPS. Three ways, and only three:
//
//  1. A deferred call recovers it. The frame whose defers are running returns
//     normally, with its named results as the deferred call left them, and the
//     caller cannot tell a panic happened. See [Runner.bashPPRecover].
//  2. It escapes the outermost Go-form frame. Nothing further can recover it,
//     so it is reported and the shell terminates with status 2, as an
//     unrecovered Go panic terminates a program. See
//     [Runner.bashPPPanicTerminate].
//  3. A deferred call runs an explicit `exit`. The script asked to terminate,
//     and it terminates with that status and no panic report — the same
//     precedence `os.Exit` has over a panic in flight.
//
// A subshell is a process boundary, so a panic never crosses one: the subshell
// carries no active panic in (it is not copied by [Runner.subshell]) and no
// Go-form frame either, so a panic raised inside one settles inside it, giving
// the subshell exit status 2 and leaving the parent to observe that status like
// any other.

// bashPPPanicStatus is the status of a shell terminated by an unrecovered
// panic. Go exits 2 for an unrecovered panic; so does this.
const bashPPPanicStatus = 2

// bashPPPanicState is the panic unwinding the Bash++ call stack.
//
// chain holds every value raised during ONE unwind, oldest first. A panic
// raised while another is unwinding becomes the active one — a recover takes
// the newest, and a report prints all unrecovered values. If that newest panic
// is recovered inside the deferred call that raised it, the older panic keeps
// unwinding; this is the recursive-panic rule exercised by Go's runtime tests.
type bashPPPanicState struct {
	active bool
	// running marks that a deferred call is executing FOR this panic. It is
	// the difference between "a panic exists" and "a panic is halting the
	// shell": the cleanups an unwind runs are ordinary statements and must be
	// allowed to run, while everything else must not. A panic raised inside a
	// cleanup clears it again, so the rest of that cleanup is abandoned too.
	running bool
	chain   []string
}

// value is the payload a recover would take: the most recent panic's.
func (p bashPPPanicState) value() string {
	if len(p.chain) == 0 {
		return ""
	}
	return p.chain[len(p.chain)-1]
}

// bashPPPanicking reports whether a panic is currently unwinding this shell.
func (r *Runner) bashPPPanicking() bool { return r.bashPPPanic.active }

// bashPPPanicHalts reports whether a panic must stop the next statement from
// running. See [bashPPPanicState.running] for the one case where it must not.
func (r *Runner) bashPPPanicHalts() bool {
	return r.bashPPPanic.active && !r.bashPPPanic.running
}

// bashPPPredeclaredCall reports which predeclared Bash++ function a call names,
// or "" for any other callee.
//
// It is consulted only AFTER the session's own functions, so a script that
// declares `func panic(...)` shadows the predeclared one, exactly as a Go
// declaration shadows a predeclared identifier.
func bashPPPredeclaredCall(c *syntax.BashPPCall) string {
	if c == nil || c.FuncLit != nil || len(c.Fun) != 1 {
		return ""
	}
	switch name := c.Fun[0].Value; name {
	case "panic", "recover":
		return name
	}
	return ""
}

// bashPPPredeclared runs a predeclared call. The second result reports whether
// it produced VALUES a caller may bind: `recover` does, and `panic` never does,
// since a call that transfers control has nothing to return to a `:=`.
//
// The arguments arrive already evaluated, which is what makes `defer panic(v)`
// panic with the value v held when the defer ran rather than when the frame
// unwound.
//
// `recover` reports through the exit status as well as through its result,
// because the payload alone cannot answer the question Go answers with nil: a
// panic value may itself be the empty string. Status 0 means a panic was
// recovered, status 1 means there was nothing to recover.
func (r *Runner) bashPPPredeclared(name string, c *syntax.BashPPCall, args []string) ([]string, bool) {
	switch name {
	case "panic":
		if len(args) != 1 || c.Ellipsis.IsValid() {
			r.errf("panic: takes exactly one argument\n")
			r.exit = exitStatus{code: 2}
			return nil, false
		}
		r.bashPPRaise(args[0])
		return nil, false
	case "recover":
		if len(args) != 0 {
			r.errf("recover: takes no arguments\n")
			r.exit = exitStatus{code: 2}
			return nil, false
		}
		value, ok := r.bashPPRecover()
		r.exit = exitStatus{}
		r.exit.oneIf(!ok)
		return []string{value}, true
	}
	return nil, false
}

// bashPPRaise starts a panic with value, abandoning the current statement.
//
// With no Go-form frame active there is nothing to unwind and no defer that
// could recover it, so the panic is reported and terminates the shell at once
// rather than pretending to look for a handler that cannot exist.
func (r *Runner) bashPPRaise(value string) {
	r.bashPPPanic.chain = append(r.bashPPPanic.chain, value)
	r.bashPPPanic.active = true
	// A panic raised inside a cleanup is a new unwind, not a continuation of
	// the one that ran the cleanup: it abandons the rest of that cleanup too.
	r.bashPPPanic.running = false
	if r.bashPPFuncActive == 0 {
		r.bashPPPanicTerminate()
		return
	}
	r.bashPPUnwind()
}

// bashPPUnwind puts the runner into the halted state a panic needs: no further
// statement runs, and every loop and compound body treats the frame as leaving.
func (r *Runner) bashPPUnwind() {
	r.exit = exitStatus{code: bashPPPanicStatus, returning: true}
}

// bashPPRecover implements Go's recover.
//
// It succeeds only in a call the unwinding frame deferred DIRECTLY: the call
// stack must be exactly one frame deeper than the frame whose defers are
// running. That single comparison covers every case the spec lists — a recover
// in the function body itself is too shallow, a recover in a function called
// BY the deferred function is too deep, and `defer recover()` never pushes a
// frame at all, so it is too shallow as well and does not stop the panic.
func (r *Runner) bashPPRecover() (string, bool) {
	if !r.bashPPPanic.active {
		return "", false
	}
	if r.bashPPDeferDepth == 0 || len(r.callStack) != r.bashPPDeferDepth {
		return "", false
	}
	last := len(r.bashPPPanic.chain) - 1
	value := r.bashPPPanic.chain[last]
	r.bashPPPanic.chain = r.bashPPPanic.chain[:last]
	r.bashPPPanic.active = len(r.bashPPPanic.chain) > 0
	// A directly deferred invocation continues after recover. When it has
	// recovered a nested panic, an older panic still exists but stays suspended
	// until this cleanup returns to the older unwind's defer runner.
	r.bashPPPanic.running = r.bashPPPanic.active
	return value, true
}

// bashPPPanicTerminate reports an unrecovered panic and terminates the shell.
//
// The report is the panic chain and nothing more. A fabricated Go stack trace
// would name frames this shell does not have, so what is printed is exactly
// what the shell knows: the value panicked with, and each value that replaced
// it while the stack unwound.
func (r *Runner) bashPPPanicTerminate() {
	var b strings.Builder
	for i, value := range r.bashPPPanic.chain {
		if i > 0 {
			b.WriteString("\t")
		}
		b.WriteString("panic: ")
		b.WriteString(value)
		b.WriteString("\n")
	}
	r.errf("%s", b.String())
	r.bashPPPanic = bashPPPanicState{}
	r.exit = exitStatus{code: bashPPPanicStatus, exiting: true}
}

// bashPPPanicSettled reports whether a hard exit has overtaken the panic, and
// discards it when so.
//
// An explicit `exit` from a deferred call is the script terminating itself on
// purpose. That outranks a panic in flight, and it suppresses the panic report
// for the same reason `os.Exit` does: the program is not crashing, it is
// leaving.
func (r *Runner) bashPPPanicSettledByExit() bool {
	if !r.bashPPPanic.active || !(r.exit.exiting || r.exit.fatalExit) {
		return false
	}
	r.bashPPPanic = bashPPPanicState{}
	return true
}
