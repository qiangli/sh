package interp

import (
	"context"
	"fmt"

	"mvdan.cc/sh/v3/syntax"
)

// Result parameters have the same zero values and storage as local variables.
// Reuse declaration rather than manufacturing an empty scalar result cell.
func (r *Runner) goSourceDeclareResults(ctx context.Context, fields []*syntax.BashPPField) bool {
	for _, field := range fields {
		for _, name := range field.Names {
			if name.Value == "_" {
				continue
			}
			r.bashPPDeclare(ctx, &syntax.BashPPDecl{Site: syntax.StartVar, Kw: &syntax.Lit{Value: "var", ValuePos: name.Pos()}, Name: name, DeclType: field.FieldType, DeclTypeExpr: field.FieldTypeExpr})
			if !r.exit.ok() || r.bashPPPanicking() {
				return false
			}
		}
	}
	return true
}

func (r *Runner) goSourceNativeAssignCall(ctx context.Context, assign *syntax.BashPPAssign) {
	values, err := r.bashPPBridgeCall(ctx, assign.Call)
	if err != nil {
		if !r.bashPPPanicking() {
			r.exit.fatal(err)
		}
		return
	}
	if r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPPanicking() {
		return
	}
	if len(values) != len(assign.Names) {
		r.exit.fatal(fmt.Errorf("%sBASHPP-EASSIGN-ARITY: %d variables but %d native results", r.bashErrPrefix(assign.Eq), len(assign.Names), len(values)))
		return
	}
	cells := make([]*bashPPCell, len(values))
	for i, value := range values {
		cells[i] = goSourceNativeValueCell(value)
		if target := r.bashPPScope.lookup(assign.Names[i].Value); target != nil {
			if _, iface := r.bashPPInterfaceType(target.declType); iface {
				// The source was typechecked, and the dependency returned its
				// actual dynamic value. Keep nil-interface vs typed-nil identity
				// while retaining the destination's static interface type.
				value.Interface = bashPPBridgeTypeText(target.declType)
				cells[i] = goSourceNativeValueCell(value)
				payload := bashPPCopyAssignmentCell(cells[i])
				cells[i].interfaceValue = &bashPPInterfaceValue{nilIface: value.Kind == "nil", cell: payload, dynamic: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Type}}}
				cells[i].declType = target.declType
				cells[i].typeName = target.typeName
			}
		}
	}
	r.bashPPCommitTupleAssign(assign, cells)
}
