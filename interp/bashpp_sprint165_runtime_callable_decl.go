// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// goSourceCallableDeclValue is the closure a `var` declaration's initializer
// denotes when it spells a function value by name — a package function, a
// bound method value, a method expression, a generic instantiation's method
// — rather than by a literal. Only the literal form bound a closure before;
// the others left the variable holding the initializer's text, so a later
// call through the variable found no function and fell through to the
// dependency dispatch (`unknown imported symbol or method: fn`). A
// non-callable initializer reports false and keeps the scalar path.
func (r *Runner) goSourceCallableDeclValue(expr syntax.BashPPExpr) (expand.Variable, bool) {
	switch expr.(type) {
	case *syntax.BashPPIdent, *syntax.BashPPSelectorExpr, *syntax.BashPPParenExpr:
	default:
		return expand.Variable{}, false
	}
	cell, handled, err := r.goSourceCallableCell(expr)
	if !handled || err != nil || cell == nil {
		return expand.Variable{}, false
	}
	if _, closure := r.bashPPClosure(cell.vr.Str); !closure {
		return expand.Variable{}, false
	}
	return cell.vr, true
}
