package interp

import (
	"fmt"
	"go/constant"

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
	for i, expr := range call.ArgExprs {
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
