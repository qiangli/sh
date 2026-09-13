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
