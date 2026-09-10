package interp

import (
	"context"
	"errors"
	"fmt"

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
		return &bashPPCell{vr: v, declType: bashPPFuncLitType(x)}, true, nil
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
	if cell, handled, err := r.goSourceNilValueCell(expr); handled {
		return cell, err
	}
	if paren, ok := expr.(*syntax.BashPPParenExpr); ok {
		return r.goSourceValueCell(paren.X)
	}
	if cell, handled, err := r.goSourceChannelValueCell(expr); handled {
		return cell, err
	}
	if cell, handled, err := r.goSourceCollectionBuiltinCell(expr); handled {
		return cell, err
	}
	if cell, handled, err := r.goSourceCallableCell(expr); handled {
		return cell, err
	}
	if call, ok := expr.(*syntax.BashPPCall); ok {
		if cell, handled, err := r.goSourceBuiltinResult(call); handled {
			return cell, err
		}
		if r.bashPPBridgeHandles(call) {
			values, err := r.bashPPBridgeCall(r.ectx, call)
			if err != nil {
				return nil, err
			}
			if len(values) != 1 {
				return nil, fmt.Errorf("Go value requires one result")
			}
			return goSourceNativeValueCell(values[0]), nil
		}
		if fn, ok := r.bashPPLookupFunc(call); ok {
			cells, err := r.goSourceCallResultCells(call, fn)
			if err != nil {
				return nil, err
			}
			if len(cells) != 1 {
				return nil, fmt.Errorf("Go value requires one result")
			}
			return cells[0], nil
		}
	}
	if r.bashPPNativeExpr(expr) {
		value, err := r.bashPPBridgeExpr(expr)
		if err != nil {
			return nil, err
		}
		return goSourceNativeValueCell(value), nil
	}
	if cell, err := r.bashPPStructuredArgCell(nil, expr); err != nil || cell != nil {
		return cell, err
	}
	v, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		return nil, err
	}
	cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(v.value)}, scalarKind: v.value.Kind(), typeName: v.typ}
	if v.typ != "" {
		cell.declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: v.typ}}
	}
	return cell, nil
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
