// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"errors"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// A nil dereference is a run-time fault, not a static refusal.
//
// Go detects a nil pointer dereference when the load or store executes and
// raises a runtime.Error ("runtime error: invalid memory address or nil
// pointer dereference") that unwinds like any other panic: deferred calls
// run, a directly deferred recover stops it, and an unrecovered one
// terminates the program with status 2. The evaluator used to detect the
// same fault and report it as a BASHPP-ENIL-DEREF refusal, which no recover
// could observe. Every detection site now hands back a [bashPPRuntimeError]
// and the expression entry points convert it into the panic Go raises.
//
// Classic Bash++ keeps its refusal wording: the error's text is unchanged
// and only Go source turns it into a panic, so scripts that never spell a
// defer or recover see exactly what they saw before.

// bashPPRuntimeError is a Go run-time panic detected during evaluation. Its
// Error text is the evaluator's refusal wording, which classic Bash++
// reports as before; runtime is the runtime.Error message Go raises.
type bashPPRuntimeError struct {
	refusal string
	runtime string
}

func (e *bashPPRuntimeError) Error() string { return e.refusal }

// bashPPRuntimeErrorText is the text of the runtime.Error Go's runtime
// reports for the fault, and therefore the value a recover observes.
func (e *bashPPRuntimeError) bashPPRuntimeErrorText() string { return "runtime error: " + e.runtime }

// errBashPPNilDereference is the fault every nil dereference site reports.
// It is a single value so that the plain-function sites, which have no
// runner to raise through, report the same fault as the runner's own.
var errBashPPNilDereference = &bashPPRuntimeError{
	refusal: "BASHPP-ENIL-DEREF: dereference of nil pointer",
	runtime: "invalid memory address or nil pointer dereference",
}

// errBashPPNilEmbeddedDereference is the same fault reached through a nil
// embedded pointer while a promoted field or method is resolved.
var errBashPPNilEmbeddedDereference = &bashPPRuntimeError{
	refusal: "BASHPP-ENIL-DEREF: dereference of nil embedded pointer",
	runtime: "invalid memory address or nil pointer dereference",
}

// goSourceRuntimeFault turns a run-time fault into the panic Go raises.
//
// Any other error, and every error in classic Bash++, is returned unchanged.
// For a fault in Go source the panic is raised here, once, and the
// interrupted sentinel is returned in its place so the statement that was
// evaluating the expression halts without reporting a second diagnostic —
// the same contract goSourceInterfaceEqual established for an uncomparable
// dynamic type.
//
// Raising is guarded against a panic already unwinding the same statement:
// several evaluation paths retry an expression through a fallback reader
// after the first reader fails, and the retry must not raise the fault a
// second time. A deferred call running for that panic is not a retry, so a
// fault raised inside one starts the new unwind Go would start.
func (r *Runner) goSourceRuntimeFault(err error) error {
	if err == nil || !r.bashPPGoSource {
		return err
	}
	var fault *bashPPRuntimeError
	if !errors.As(err, &fault) {
		return err
	}
	if r.bashPPPanicHalts() {
		return errBashPPScalarInterrupted
	}
	r.goSourceRuntimePanic(fault.bashPPRuntimeErrorText())
	return errBashPPScalarInterrupted
}

// goSourceRuntimePanic raises a runtime.Error panic at the statement that
// is executing, retaining the interpreted frames as an explicit panic call
// would, so an unrecovered fault reports the same Go-shaped traceback.
func (r *Runner) goSourceRuntimePanic(text string) {
	r.bashPPPanic.traceSource = r.filename
	r.bashPPPanic.traceLine = r.curStmtPos.Line()
	r.bashPPPanic.traceFrames = r.bashPPPanic.traceFrames[:0]
	for _, frame := range r.callStack {
		r.bashPPPanic.traceFrames = append(r.bashPPPanic.traceFrames, frame.funcName)
	}
	r.bashPPRaise(text)
}

// bashPPReportNilDereference is the statement-level form of the fault, for
// the assignment and declaration paths that report their own diagnostics
// instead of returning an error.
func (r *Runner) bashPPReportNilDereference() {
	if err := r.goSourceRuntimeFault(errBashPPNilDereference); errors.Is(err, errBashPPScalarInterrupted) {
		return
	}
	r.errf("%v\n", errBashPPNilDereference)
	r.exit = exitStatus{code: 2}
}

// goSourceRangePointerArray ranges over a pointer to an array, which Go
// permits with the array's own iteration: `for i, v := range p` reads p's
// elements in place. Two details are the whole reason it is its own path:
//
//   - The elements are read through the pointer at each iteration rather
//     than from a copy, since ranging a pointer never copies the array.
//   - With at most one iteration variable, len(*p) is a constant and the
//     spec says the range expression is not evaluated at all, so a nil p
//     ranges its indices without faulting. With two, *p is evaluated and a
//     nil p is the nil dereference it always was.
//
// It reports whether the operand was such a pointer; anything else is left
// to the collection ranges.
func (r *Runner) goSourceRangePointerArray(ctx context.Context, rng *syntax.BashPPRange, value any, meta *bashPPCollectionMeta) bool {
	if !r.bashPPGoSource || meta == nil || meta.kind != "pointer" {
		return false
	}
	pointerType, ok := r.bashPPPointerType(meta.typ)
	if !ok {
		return false
	}
	array, ok := r.bashPPUnderlyingType(pointerType.Element).(*syntax.BashPPCollectionType)
	if !ok || array.Kind != "array" || array.Length == nil {
		return false
	}
	ptr, _ := value.(*bashPPPointer)
	if ptr == nil {
		if len(rng.Names) > 1 {
			r.goSourceRuntimeFault(errBashPPNilDereference)
			return true
		}
		length, err := r.bashPPArrayLength(array.Length.Value)
		if err != nil {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: cannot range over %s: %v", bashPPTypeText(meta.typ), err)
			return true
		}
		for i := 0; i < length; i++ {
			if !r.bashPPRangeIteration(ctx, rng, i, bashPPRangeNamedType("int"), nil, nil, array.Element) {
				return true
			}
		}
		return true
	}
	target, targetMeta, _, err := ptr.read()
	if err != nil {
		if err = r.goSourceRuntimeFault(err); !errors.Is(err, errBashPPScalarInterrupted) {
			r.bashPPRangeError(rng, "%v", err)
		}
		return true
	}
	sequence, _ := target.([]any)
	for i := 0; i < len(sequence); i++ {
		var elemMeta *bashPPCollectionMeta
		if targetMeta != nil && i < len(targetMeta.sequence) {
			elemMeta = targetMeta.sequence[i]
		}
		if !r.bashPPRangeIteration(ctx, rng, i, bashPPRangeNamedType("int"), sequence[i], elemMeta, array.Element) {
			return true
		}
	}
	return true
}

// goSourceIndexedPointee follows the implicit dereference Go inserts when a
// pointer to an array is indexed or sliced: `p[i]` and `p[lo:hi]` read
// through p. A nil p is the nil dereference; any other value is returned as
// it was.
func (r *Runner) goSourceIndexedPointee(value any, meta *bashPPCollectionMeta) (any, *bashPPCollectionMeta, error) {
	if !r.bashPPGoSource || meta == nil || meta.kind != "pointer" {
		return value, meta, nil
	}
	pointerType, ok := r.bashPPPointerType(meta.typ)
	if !ok {
		return value, meta, nil
	}
	if array, ok := r.bashPPUnderlyingType(pointerType.Element).(*syntax.BashPPCollectionType); !ok || array.Kind != "array" {
		return value, meta, nil
	}
	ptr, _ := value.(*bashPPPointer)
	if ptr == nil {
		return nil, nil, r.goSourceRuntimeFault(errBashPPNilDereference)
	}
	target, targetMeta, _, err := ptr.read()
	if err != nil {
		return nil, nil, r.goSourceRuntimeFault(err)
	}
	return target, targetMeta, nil
}

// goSourceStructuredIdent reports whether an identifier names a pointer or
// structured variable — one whose value is not its scalar spelling.
func (r *Runner) goSourceStructuredIdent(id *syntax.BashPPIdent) bool {
	if !r.bashPPGoSource || r.bashPPScope == nil {
		return false
	}
	cell := r.bashPPScope.lookup(id.Name.Value)
	return cell != nil && (cell.pointer || cell.vr.Kind == expand.Object)
}

// goSourceNilValueReceiver raises the fault Go raises when a value-receiver
// method is selected through a nil pointer: the selection dereferences the
// pointer to copy the receiver, and a nil one is the nil dereference. It
// reports whether the fault was raised, which classic Bash++ declines so
// its refusal stands.
func (r *Runner) goSourceNilValueReceiver() bool {
	if !r.bashPPGoSource {
		return false
	}
	r.goSourceRuntimeFault(errBashPPNilDereference)
	return true
}

// goSourceNilInterfaceOperand reports whether a selector's operand is a
// variable holding a nil interface, whose method selection is Go's nil
// dereference.
func (r *Runner) goSourceNilInterfaceOperand(expr syntax.BashPPExpr) bool {
	if !r.bashPPGoSource || r.bashPPScope == nil {
		return false
	}
	for {
		paren, ok := expr.(*syntax.BashPPParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	id, ok := expr.(*syntax.BashPPIdent)
	if !ok {
		return false
	}
	cell := r.bashPPScope.lookup(id.Name.Value)
	return cell != nil && cell.interfaceValue != nil && cell.interfaceValue.nilIface && cell.vr.Kind != expand.Object
}

// bashPPReportFault reports an error a method binding met: a run-time fault
// in Go source is raised as its panic, anything else is the diagnostic it
// was, with the status a binding failure reports.
func (r *Runner) bashPPReportFault(err error) {
	if err = r.goSourceRuntimeFault(err); errors.Is(err, errBashPPScalarInterrupted) {
		return
	}
	r.errf("%v\n", err)
	r.exit.code = 2
}

// goSourceNestedDeferRunning decides whether a panic keeps running — not
// halting — when a frame returns. Only a frame called by a deferred call of
// the running panic entered with running set; it stays set on return unless
// a panic raised inside the frame is still unwinding (the chain is longer
// than at entry), which abandons the rest of the deferred call as Go
// abandons it. A frame entered with no panic running never sets it: its own
// defers ran for a panic that is halting again when they finish.
func (r *Runner) goSourceNestedDeferRunning(enteredRunning bool, enteredChain int) bool {
	return enteredRunning && r.bashPPPanic.active && len(r.bashPPPanic.chain) <= enteredChain
}
