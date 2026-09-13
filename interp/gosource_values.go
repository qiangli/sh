package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func goSourceNativeValueCell(value bashPPBridgeValue) *bashPPCell {
	if scalar, err := value.scalar(); err == nil {
		return &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(scalar.value)}, scalarKind: scalar.value.Kind(), typeName: scalar.typ, declType: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: scalar.typ}}}
	}
	cell := &bashPPCell{vr: expand.NewObject(&value), typeName: value.Type}
	if value.Interface != "" {
		cell.declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Interface}}
		payload := &bashPPCell{vr: expand.NewObject(&value), typeName: value.Type}
		cell.interfaceValue = &bashPPInterfaceValue{
			nilIface: value.Kind == "nil",
			cell:     payload,
			dynamic:  &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Type}},
		}
	}
	return cell
}
func (r *Runner) goSourceCallableCell(expr syntax.BashPPExpr) (*bashPPCell, bool, error) {
	if !r.bashPPGoSource || expr == nil {
		return nil, false, nil
	}
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.goSourceCallableCell(x.X)
	case *syntax.BashPPFuncLit:
		_, v := r.bashPPMakeClosure(x)
		return &bashPPCell{vr: v, declType: r.bashPPBindTypeExpr(bashPPFuncLitType(x))}, true, nil
	case *syntax.BashPPIdent:
		if cell := r.bashPPScope.lookup(x.Name.Value); cell != nil {
			if _, ok := r.bashPPClosure(cell.vr.Str); ok {
				return bashPPCopyAssignmentCell(cell), true, nil
			}
			// A non-callable local binding shadows the package function too.
			return nil, false, nil
		}
		if fn := r.bashPPFuncs[x.Name.Value]; fn != nil {
			return &bashPPCell{vr: r.bashPPStoreFunc(fn)}, true, nil
		}
	case *syntax.BashPPSelectorExpr:
		if x.MethodValue && !r.bashPPNativeExpr(x.X) {
			cell, err := r.goSourceLocalMethodValue(x)
			return cell, true, err
		}
		if cell, handled, err := r.goSourceMethodExprCell(x); handled {
			return cell, true, err
		}
		if x.FuncType == nil {
			return nil, false, nil
		}
		if id, ok := x.X.(*syntax.BashPPIdent); ok {
			if _, imported := r.bashPPImports[id.Name.Value]; imported {
				v, err := r.bashPPBridgeExpr(x)
				if err != nil {
					return nil, true, err
				}
				fn := &bashPPFunc{native: &v, lit: &syntax.BashPPFuncLit{Params: x.FuncType.Params, Results: x.FuncType.Results}}
				return &bashPPCell{vr: r.bashPPStoreFunc(fn), declType: x.FuncType}, true, nil
			}
		}
	}
	return nil, false, nil
}
func (r *Runner) goSourceValueCell(expr syntax.BashPPExpr) (*bashPPCell, error) {
	cells, err := r.goSourceValueCells(expr, false)
	if err != nil {
		return nil, err
	}
	return cells[0], nil
}

// goSourceValueCells evaluates one expression to its result cells. A call is
// the only expression with more than one: with spread set it contributes
// every result it returns, which is how `f(g())` hands g's results to f;
// without it a call must yield exactly one value. Every other expression is
// one cell.
func (r *Runner) goSourceValueCells(expr syntax.BashPPExpr, spread bool) ([]*bashPPCell, error) {
	one := func(cell *bashPPCell, err error) ([]*bashPPCell, error) {
		if err != nil {
			return nil, err
		}
		return []*bashPPCell{cell}, nil
	}
	if cell, handled, err := r.goSourceNilValueCell(expr); handled {
		return one(cell, err)
	}
	if paren, ok := expr.(*syntax.BashPPParenExpr); ok {
		return r.goSourceValueCells(paren.X, spread)
	}
	if cell, handled, err := r.goSourceChannelValueCell(expr); handled {
		return one(cell, err)
	}
	if cell, handled, err := r.goSourceCollectionBuiltinCell(expr); handled {
		return one(cell, err)
	}
	if cell, handled, err := r.goSourceCallableCell(expr); handled {
		return one(cell, err)
	}
	if call, ok := expr.(*syntax.BashPPCall); ok {
		if cell, handled, err := r.goSourceBuiltinResult(call); handled {
			return one(cell, err)
		}
		if r.bashPPBridgeHandles(call) {
			values, err := r.bashPPBridgeCall(r.ectx, call)
			if err != nil {
				return nil, err
			}
			if len(values) != 1 && !spread {
				return nil, fmt.Errorf("Go value requires one result")
			}
			cells := make([]*bashPPCell, len(values))
			for i, value := range values {
				cells[i] = goSourceNativeValueCell(value)
			}
			return cells, nil
		}
		if fn, ok := r.bashPPLookupFunc(call); ok {
			cells, err := r.goSourceCallResultCells(call, fn)
			if err != nil {
				return nil, err
			}
			if len(cells) != 1 && !spread {
				return nil, fmt.Errorf("Go value requires one result")
			}
			return cells, nil
		} else if r.bashPPPanicHalts() || r.exit.code != 0 {
			// The callee lookup itself raised — a method selected on a nil
			// interface — or reported; evaluating the call again below would
			// raise a second panic inside the unwinding of the first.
			return nil, errBashPPScalarInterrupted
		}
	}
	if r.bashPPNativeExpr(expr) {
		value, err := r.bashPPBridgeExpr(expr)
		if err != nil {
			return nil, err
		}
		return one(goSourceNativeValueCell(value), nil)
	}
	if cell, err := r.bashPPStructuredArgCell(nil, expr); err != nil || cell != nil {
		return one(cell, err)
	}
	v, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		return nil, err
	}
	cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(v.value)}, scalarKind: v.value.Kind(), typeName: v.typ}
	if v.typ != "" {
		cell.declType, cell.typeName = bashPPScalarNamedType(v.typ)
	}
	return one(cell, nil)
}
func (r *Runner) goSourceValues(exprs []syntax.BashPPExpr) ([]*bashPPCell, bool) {
	cells := make([]*bashPPCell, len(exprs))
	for i, expr := range exprs {
		cell, err := r.goSourceValueCell(expr)
		if err != nil {
			if !errors.Is(err, errBashPPScalarInterrupted) {
				r.errf("%s%v\n", r.bashErrPrefix(expr.Pos()), err)
				r.exit = exitStatus{code: 2}
				r.bashPPShortFailureSeq++
			}
			return nil, false
		}
		cells[i] = bashPPCopyAssignmentCell(cell)
	}
	return cells, true
}
func (r *Runner) goSourceParallelDecl(d *syntax.BashPPShortDecl) {
	if len(d.Lhs) != len(d.RhsExprs) {
		r.errf("Go assignment arity mismatch\n")
		r.exit = exitStatus{code: 2}
		return
	}
	cells, ok := r.goSourceValues(d.RhsExprs)
	if !ok {
		return
	}
	for i, lhs := range d.Lhs {
		if lhs.Value == "_" {
			continue
		}
		r.bashPPDeclareName(lhs.Value, cells[i].vr)
		if target := r.bashPPScope.lookup(lhs.Value); target != nil {
			*target = *cells[i]
		}
	}
}
func (r *Runner) goSourceReturnValues(exprs []syntax.BashPPExpr) {
	cells, ok := r.goSourceValues(exprs)
	if !ok {
		return
	}
	values := make([]string, len(cells))
	for i, c := range cells {
		values[i] = c.vr.String()
	}
	r.bashPPReturn = bashPPReturnState{active: true, values: values, cells: cells}
	r.exit.returning = true
}
func (r *Runner) goSourceInvokeNative(ctx context.Context, fn *bashPPFunc, args []string, cells []*bashPPCell) []string {
	req, err := r.bashPPEvalRequest()
	if err != nil {
		r.exit.fatal(err)
		return nil
	}
	q := bashPPBridgeRequest{Op: "call", Receiver: fn.native}
	for i, arg := range args {
		var cell *bashPPCell
		if i < len(cells) {
			cell = cells[i]
		}
		if cell == nil {
			cell = &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: arg}}
		}
		value, err := r.bashPPBridgeCell(cell)
		if err != nil {
			r.exit.fatal(err)
			return nil
		}
		q.Args = append(q.Args, value)
	}
	values, err := r.bashPPNativeRequest(ctx, req, q)
	if err != nil {
		r.exit.fatal(err)
		return nil
	}
	results := make([]string, len(values))
	r.bashPPResultCells = make([]*bashPPCell, len(values))
	for i, value := range values {
		cell := goSourceNativeValueCell(value)
		r.bashPPResultCells[i] = cell
		results[i] = cell.vr.String()
	}
	return results
}

// bashPPGoSourceComputedNativeCall dispatches a call whose computed callee
// evaluates to a dependency's function value — `m.Func.Interface().(func(M))(v)`,
// `fs[i]()` where fs holds native handles. The statement and expression callee
// branches resolve only local closures and named cells by themselves; a handle
// reached through a type assertion, index, or any other computed expression has
// no name to look up, so it is evaluated here and invoked on the dependency.
//
// It reports whether it claimed the call. A handle that is not a function
// value, or a callee that does not evaluate to one, is declined so the ordinary
// "computed callee is not a function" diagnostic still applies.
func (r *Runner) bashPPGoSourceComputedNativeCall(ctx context.Context, c *syntax.BashPPCall) bool {
	if !r.bashPPGoSource || c == nil || c.CalleeExpr == nil {
		return false
	}
	cell, err := r.goSourceValueCell(c.CalleeExpr)
	if err != nil || cell == nil {
		return false
	}
	value, err := r.bashPPBridgeCell(cell)
	if err != nil {
		return false
	}
	if value.Kind != "handle" || !(value.Function || strings.HasPrefix(value.Type, "func(")) {
		return false
	}
	fn := &bashPPFunc{native: &value}
	if sig := bashPPComputedCalleeSignature(c.CalleeExpr, cell); sig != nil {
		fn.lit = &syntax.BashPPFuncLit{Params: sig.Params, Results: sig.Results}
	}
	args, ok := r.bashPPCallValues(c, fn)
	if !ok {
		return true
	}
	r.bashPPInvoke(ctx, fn, args)
	return true
}

// bashPPComputedCalleeSignature recovers the concrete signature a computed
// callee was asserted or declared to have, so argument binding has the
// parameter types the dependency expects. A type assertion `.(func(...))` spells
// it directly; otherwise the evaluated cell's declared type may carry it.
func bashPPComputedCalleeSignature(expr syntax.BashPPExpr, cell *bashPPCell) *syntax.BashPPFuncType {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return bashPPComputedCalleeSignature(x.X, cell)
	case *syntax.BashPPTypeAssertExpr:
		if ft, ok := x.Assert.(*syntax.BashPPFuncType); ok {
			return ft
		}
	}
	if cell != nil {
		if ft, ok := cell.declType.(*syntax.BashPPFuncType); ok {
			return ft
		}
	}
	return nil
}
