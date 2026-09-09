package interp

import (
	"fmt"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPGoSourceTupleCall evaluates an interpreted callable exactly once and
// retains every result cell for Go's sole-multi-value-argument rule.
func (r *Runner) bashPPGoSourceTupleCall(call *syntax.BashPPCall) ([]*bashPPCell, error) {
	fn, ok := r.bashPPLookupFunc(call)
	if !ok {
		return nil, fmt.Errorf("gosource: undefined interpreted callable")
	}
	var args []string
	if call.ArgExprs != nil {
		cells := make([]*bashPPCell, len(call.ArgExprs))
		for i, expr := range call.ArgExprs {
			value, err := r.bashPPEvalScalarExpr(expr)
			if err != nil {
				return nil, err
			}
			text := bashPPScalarString(value.value)
			args = append(args, text)
			cells[i] = &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: text}, scalarKind: value.value.Kind()}
			if value.typ != "" {
				cells[i].declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.typ}}
			}
		}
		args, ok = r.bashPPBindCall(fn, args, nil, cells, nil, len(args))
	} else {
		args, ok = r.bashPPCallValues(call, fn)
	}
	if !ok {
		return nil, errBashPPScalarInterrupted
	}
	previous := r.bashPPResultCells
	defer func() { r.bashPPResultCells = previous }()
	failure := r.bashPPShortFailureSeq
	values := r.bashPPInvoke(r.ectx, fn, args)
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != failure || len(values) != len(r.bashPPResultCells) {
		return nil, errBashPPScalarInterrupted
	}
	results := make([]*bashPPCell, len(values))
	for i, c := range r.bashPPResultCells {
		results[i] = bashPPCopyAssignmentCell(c)
	}
	return results, nil
}
