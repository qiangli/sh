package interp

import "mvdan.cc/sh/v3/syntax"

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
