// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Function values as interface operands.
//
// A function value has no scalar spelling: a nil `var f func()` renders as
// the empty string and a closure as its handle. Passed to an interface
// parameter or compared through one, such a value must keep its function
// type as the dynamic type — Go boxes `f` as a `func()`, so `x == x` on the
// boxed value panics with `comparing uncomparable type func()` whether f is
// nil, a literal or a declared function.

// bashPPFuncTypedCell reports whether a binding is declared with a function
// type, so an argument naming it travels as the cell rather than as text.
func (r *Runner) bashPPFuncTypedCell(cell *bashPPCell) bool {
	if cell == nil || cell.declType == nil {
		return false
	}
	_, ok := r.bashPPUnderlyingType(cell.declType).(*syntax.BashPPFuncType)
	return ok
}

// bashPPFuncValueType is the dynamic type of a cell holding a function
// value by handle — a closure's literal signature or a declared function's —
// or nil when the cell holds no function value.
func (r *Runner) bashPPFuncValueType(cell *bashPPCell) syntax.BashPPTypeExpr {
	if cell == nil || cell.vr.Kind != expand.String {
		return nil
	}
	fn, ok := r.bashPPClosure(cell.vr.Str)
	if !ok {
		return nil
	}
	switch {
	case fn.lit != nil:
		return bashPPFuncLitType(fn.lit)
	case fn.decl != nil:
		return &syntax.BashPPFuncType{
			Func: fn.decl.Kw.Pos(), Lparen: fn.decl.Lparen, Rparen: fn.decl.Rparen,
			Params: fn.decl.Params, Results: fn.decl.Results,
			ResLparen: fn.decl.ResLparen, ResRparen: fn.decl.ResRparen,
		}
	}
	return nil
}

// bashPPNilFuncCall reports whether a call's callee is a nil function value:
// a func-typed variable holding nil, or a conversion of nil to a func type
// (`((func())(nil))()`). Calling one is a run-time error in Go, raised when
// the call — or the deferred call — runs.
func (r *Runner) bashPPNilFuncCall(c *syntax.BashPPCall) bool {
	if c == nil || !r.bashPPGoSource {
		return false
	}
	if c.CalleeExpr != nil {
		return r.bashPPNilFuncConversion(c.CalleeExpr)
	}
	if len(c.Fun) != 1 || r.bashPPScope == nil || r.bashPPFuncs[c.Fun[0].Value] != nil {
		return false
	}
	cell := r.bashPPScope.lookup(c.Fun[0].Value)
	if !r.bashPPFuncTypedCell(cell) {
		return false
	}
	switch cell.vr.Kind {
	case expand.String:
		_, closure := r.bashPPClosure(cell.vr.Str)
		return cell.vr.Str == "" && !closure
	case expand.Object:
		native, ok := cell.vr.Obj.(*bashPPBridgeValue)
		return ok && native != nil && native.Kind == "nil"
	}
	return false
}

func (r *Runner) bashPPNilFuncConversion(expr syntax.BashPPExpr) bool {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.bashPPNilFuncConversion(x.X)
	case *syntax.BashPPConvertExpr:
		id, ok := x.X.(*syntax.BashPPIdent)
		if !ok || id.Name.Value != "nil" || r.bashPPScope.lookup("nil") != nil {
			return false
		}
		target := r.bashPPBindTypeExpr(r.bashPPConvertTarget(x))
		if target == nil {
			return false
		}
		_, isFunc := r.bashPPUnderlyingType(target).(*syntax.BashPPFuncType)
		return isFunc
	}
	return false
}

// bashPPRaiseNilFuncCall raises the run-time error a call of a nil function
// value produces.
func (r *Runner) bashPPRaiseNilFuncCall() {
	r.bashPPRaiseRuntimeError(bashPPRuntimeErrorString, bashPPRuntimeErrorMessage+"invalid memory address or nil pointer dereference")
}
