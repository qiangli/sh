package interp

import (
	"fmt"
	"go/constant"
	"strconv"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// goSourceCaptureDeferredClose resolves the builtin and evaluates its operand
// at registration time. The saved channel capability survives reassignment of
// the operand's variable; the close (and any nil/closed panic) happens only
// while unwinding the original interpreted function's defer stack.
func (r *Runner) goSourceCaptureDeferredClose(call *syntax.BashPPCall) (func(), bool) {
	if !r.bashPPGoSource || call == nil || call.FuncLit != nil || call.CalleeExpr != nil || len(call.Fun) != 1 || call.Fun[0].Value != "close" {
		return nil, false
	}
	if r.bashPPFuncs["close"] != nil || (r.bashPPScope != nil && r.bashPPScope.lookup("close") != nil) {
		return nil, false
	}
	if len(call.Args) != 1 || len(call.ArgExprs) != 1 || call.ArgExprs[0] == nil {
		r.errf("gosource: deferred close requires one positioned channel operand\n")
		r.exit = exitStatus{code: 2}
		return nil, true
	}
	channel, ok := r.goSourceChannelOperand(call.ArgExprs[0], nil, "close")
	if !ok {
		return nil, true
	}
	return func() { r.goSourceCloseChannelValue(channel) }, true
}

// goSourceCaptureDeferredValueBuiltin fixes the same defer-time evaluation
// rule for the predeclared builtins implemented over interpreter cells. The
// builtin still runs during unwind, but it sees the argument cells produced
// where the defer statement executed, including receive expressions.
func (r *Runner) goSourceCaptureDeferredValueBuiltin(call *syntax.BashPPCall) (*syntax.BashPPCall, bool) {
	if !r.bashPPGoSource || call == nil || call.FuncLit != nil || call.CalleeExpr != nil || len(call.Fun) != 1 {
		return nil, false
	}
	name := bashPPPredeclaredCall(call)
	if !bashPPValueBuiltin(name) {
		return nil, false
	}
	if r.bashPPFuncs[name] != nil || (r.bashPPScope != nil && r.bashPPScope.lookup(name) != nil) {
		return nil, false
	}
	if len(call.Args) != len(call.ArgExprs) {
		r.errf("gosource: deferred %s requires positioned operands\n", name)
		r.exit = exitStatus{code: 2}
		return nil, true
	}
	captured := *call
	captured.Args = append([]*syntax.Word(nil), call.Args...)
	captured.ArgExprs = nil
	printing := name == "print" || name == "println"
	if printing {
		captured.ArgExprs = make([]syntax.BashPPExpr, len(call.Args))
	}
	// A deferred print operand is only ever printed, so its text is fixed
	// here in Go's print form — a float64 0.1 as `0.1`, not its exact
	// rational `1/10`; a nil slice as `[0/0]0x0` — and carried to the unwind
	// as a string literal the direct print path renders verbatim.
	printText := func(i int, text string) {
		captured.Args[i] = &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{
			Left: call.Args[i].Pos(), Right: call.Args[i].End(), Value: text,
		}}}
		captured.ArgExprs[i] = &syntax.BashPPBasicLit{Kind: "STRING", Value: &syntax.Lit{
			Value: strconv.Quote(text), ValuePos: call.Args[i].Pos(), ValueEnd: call.Args[i].End(),
		}}
	}
	for i, expr := range call.ArgExprs {
		if printing && r.goSourcePrintReferenceOperand(expr) {
			text, err := r.goSourcePrintReference(expr)
			if err != nil {
				if !r.bashPPPanicking() {
					r.exit.fatal(err)
				}
				return nil, true
			}
			printText(i, text)
			continue
		}
		cell, err := r.goSourceValueCell(expr)
		if err != nil {
			if !r.bashPPPanicking() {
				r.exit.fatal(err)
			}
			return nil, true
		}
		if cell == nil {
			r.errf("%sgosource: deferred %s argument has no value\n", r.bashErrPrefix(call.Args[i].Pos()), name)
			r.exit = exitStatus{code: 2}
			return nil, true
		}
		if cell.vr.Kind != expand.Object && cell.channel == nil && cell.interfaceValue == nil && !cell.pointer {
			scalar := r.bashPPScalarFromCell(cell)
			if scalar.value == nil || scalar.value.Kind() == constant.Unknown {
				r.errf("%sgosource: deferred %s scalar argument has no value\n", r.bashErrPrefix(call.Args[i].Pos()), name)
				r.exit = exitStatus{code: 2}
				return nil, true
			}
			if printing {
				printText(i, r.goSourcePrintScalar(scalar))
				continue
			}
			captured.Args[i] = &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{
				Left: call.Args[i].Pos(), Right: call.Args[i].End(), Value: bashPPScalarString(scalar.value),
			}}}
			continue
		}
		if r.bashPPScope == nil {
			r.errf("gosource: deferred %s has no lexical scope for captured argument\n", name)
			r.exit = exitStatus{code: 2}
			return nil, true
		}
		argName := fmt.Sprintf("bashPPDeferredArg_%d_%d_%d", uint(call.Pos().Offset()), len(r.bashPPDeferStack), i)
		copy := bashPPCopyAssignmentCell(cell)
		r.bashPPDeclareName(argName, copy.vr)
		target := r.bashPPScope.lookup(argName)
		if target == nil {
			r.errf("gosource: deferred %s could not retain captured argument\n", name)
			r.exit = exitStatus{code: 2}
			return nil, true
		}
		*target = *copy
		captured.Args[i] = &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{
			Value: argName, ValuePos: call.Args[i].Pos(), ValueEnd: call.Args[i].End(),
		}}}
	}
	return &captured, true
}
