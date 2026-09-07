// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type bashPPInterfaceValue struct {
	dynamic  syntax.BashPPTypeExpr
	cell     *bashPPCell
	nilIface bool
}

func (r *Runner) bashPPInterfaceType(typ syntax.BashPPTypeExpr) (*syntax.BashPPInterfaceType, bool) {
	if iface, ok := typ.(*syntax.BashPPInterfaceType); ok {
		return iface, true
	}
	if named, ok := typ.(*syntax.BashPPNamedType); ok {
		if decl, found := r.bashPPTypes[named.Name.Value]; found {
			if iface, ok := decl.typeExpr.(*syntax.BashPPInterfaceType); ok {
				return iface, true
			}
		}
	}
	return nil, false
}

func (r *Runner) bashPPValidateInterfaceType(name string, iface *syntax.BashPPInterfaceType) error {
	seen := make(map[string]bool, len(iface.Methods))
	for _, spec := range iface.Methods {
		if spec.Name == nil || !syntax.ValidName(spec.Name.Value) {
			return fmt.Errorf("BASHPP-EINTERFACE-METHOD: interface %s has invalid method name", name)
		}
		if seen[spec.Name.Value] {
			return fmt.Errorf("BASHPP-EINTERFACE-DUPLICATE: interface %s declares method %s more than once", name, spec.Name.Value)
		}
		seen[spec.Name.Value] = true
	}
	return nil
}

func (r *Runner) bashPPImplements(actual syntax.BashPPTypeExpr, iface *syntax.BashPPInterfaceType) error {
	typeName, pointer := bashPPInterfaceMethodOwner(actual)
	if typeName == "" {
		return fmt.Errorf("BASHPP-EINTERFACE-IMPOSSIBLE: %s cannot implement interface", bashPPTypeText(actual))
	}
	methods := r.bashPPMethods[typeName]
	for _, spec := range iface.Methods {
		fn := methods[spec.Name.Value]
		if fn == nil || (!pointer && fn.decl.Receiver.Pointer) {
			return fmt.Errorf("BASHPP-EINTERFACE-MISSING: %s does not implement interface (missing method %s)", bashPPTypeText(actual), spec.Name.Value)
		}
		if !bashPPMethodSpecMatches(fn.decl, spec) {
			return fmt.Errorf("BASHPP-EINTERFACE-SIGNATURE: %s method %s has wrong signature", bashPPTypeText(actual), spec.Name.Value)
		}
	}
	return nil
}

func bashPPInterfaceMethodOwner(typ syntax.BashPPTypeExpr) (string, bool) {
	switch x := typ.(type) {
	case *syntax.BashPPNamedType:
		return x.Name.Value, false
	case *syntax.BashPPPointerType:
		if named, ok := x.Element.(*syntax.BashPPNamedType); ok {
			return named.Name.Value, true
		}
	}
	return "", false
}

func bashPPMethodSpecMatches(fn *syntax.BashPPFuncDecl, spec *syntax.BashPPMethodSpec) bool {
	return bashPPFieldsSignature(fn.Params) == bashPPFieldsSignature(spec.Params) &&
		bashPPFieldsSignature(fn.Results) == bashPPFieldsSignature(spec.Results)
}

func bashPPFieldsSignature(fields []*syntax.BashPPField) string {
	var b strings.Builder
	for i, field := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		if field.Variadic() {
			b.WriteString("...")
		}
		if field.FieldType != nil {
			b.WriteString(field.FieldType.Value)
		}
	}
	return b.String()
}

func (r *Runner) bashPPMakeInterfaceValue(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (*bashPPInterfaceValue, expand.Variable, error) {
	if id, ok := expr.(*syntax.BashPPIdent); ok && id.Name.Value == "nil" {
		return &bashPPInterfaceValue{nilIface: true}, expand.Variable{Set: true, Kind: expand.String}, nil
	}
	cell, actual, err := r.bashPPCellForInterfaceExpr(expr)
	if err != nil {
		return nil, expand.Variable{}, err
	}
	iface, _ := r.bashPPInterfaceType(expected)
	if iface == nil {
		return nil, expand.Variable{}, fmt.Errorf("BASHPP-EINTERFACE-TYPE: %s is not an interface", bashPPTypeText(expected))
	}
	if err := r.bashPPImplements(actual, iface); err != nil {
		return nil, expand.Variable{}, err
	}
	stored := bashPPCopyInterfaceCell(cell)
	if stored.pointer && stored.pointerValue == nil {
		stored.vr = expand.Variable{Set: true, Kind: expand.String}
	}
	return &bashPPInterfaceValue{dynamic: actual, cell: stored}, stored.vr, nil
}

// bashPPCopyInterfaceCell captures the dynamic value at assignment time.
// Structs and arrays are values and therefore need their own payload, while
// pointers, maps, and slices deliberately retain the identities they carry.
func bashPPCopyInterfaceCell(cell *bashPPCell) *bashPPCell {
	stored := *cell
	stored.interfaceValue = nil
	if cell.vr.Kind == expand.Object && bashPPValueMeta(bashPPCellMeta(cell)) {
		value, meta := bashPPCopyArrayValue(cell.vr.Obj, bashPPCellMeta(cell))
		stored.vr = expand.NewObject(value)
		stored.valueMeta = meta
		stored.object = &bashPPObjectIdentity{collection: meta}
	}
	return &stored
}

func (r *Runner) bashPPCellForInterfaceExpr(expr syntax.BashPPExpr) (*bashPPCell, syntax.BashPPTypeExpr, error) {
	if id, ok := expr.(*syntax.BashPPIdent); ok {
		cell := r.bashPPScope.lookup(id.Name.Value)
		if cell == nil {
			return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: undefined value %s", id.Name.Value)
		}
		if cell.interfaceValue != nil {
			if cell.interfaceValue.nilIface {
				return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-NIL: nil interface has no dynamic type")
			}
			return cell.interfaceValue.cell, cell.interfaceValue.dynamic, nil
		}
		actual := cell.declType
		if actual == nil && cell.typeName != "" {
			actual = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: cell.typeName}}
		}
		if actual == nil {
			return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: %s has no dynamic type", id.Name.Value)
		}
		return cell, actual, nil
	}
	return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: interface assignment requires a named value")
}

func (r *Runner) bashPPTypeAssert(assert *syntax.BashPPTypeAssertExpr, commaOK bool) ([]string, *bashPPCell, error) {
	id, ok := assert.X.(*syntax.BashPPIdent)
	if !ok {
		return nil, nil, fmt.Errorf("BASHPP-EASSERT-OPERAND: type assertion operand must be an interface")
	}
	cell := r.bashPPScope.lookup(id.Name.Value)
	if cell == nil || cell.interfaceValue == nil {
		return nil, nil, fmt.Errorf("BASHPP-EASSERT-OPERAND: %s is not an interface", id.Name.Value)
	}
	iv := cell.interfaceValue
	matched := !iv.nilIface && bashPPTypeText(iv.dynamic) == bashPPTypeText(assert.Assert)
	if !matched {
		if commaOK {
			value, meta := r.bashPPZeroValue(assert.Assert)
			zero := &bashPPCell{declType: assert.Assert}
			if named, ok := assert.Assert.(*syntax.BashPPNamedType); ok {
				zero.typeName = named.Name.Value
			}
			bashPPStoreCellValue(zero, value, meta)
			return []string{zero.vr.Str, "false"}, zero, nil
		}
		return nil, nil, fmt.Errorf("BASHPP-EASSERT-FAIL: interface value has dynamic type %s, not %s", bashPPTypeText(iv.dynamic), bashPPTypeText(assert.Assert))
	}
	return []string{iv.cell.vr.Str, "true"}, iv.cell, nil
}

func (r *Runner) bashPPTypeSwitch(ctx context.Context, sw *syntax.BashPPSwitch) {
	decl, _ := sw.Init.(*syntax.BashPPShortDecl)
	assert, _ := decl.Expr.(*syntax.BashPPTypeAssertExpr)
	id, ok := assert.X.(*syntax.BashPPIdent)
	if !ok {
		r.errf("BASHPP-ETYPESWITCH-OPERAND: type switch operand must be an interface\n")
		r.exit = exitStatus{code: 2}
		return
	}
	cell := r.bashPPScope.lookup(id.Name.Value)
	if cell == nil || cell.interfaceValue == nil {
		r.errf("BASHPP-ETYPESWITCH-OPERAND: %s is not an interface\n", id.Name.Value)
		r.exit = exitStatus{code: 2}
		return
	}
	iv := cell.interfaceValue
	selected, defaultArm := -1, -1
	for armIndex, arm := range sw.Arms {
		if len(arm.Exprs) == 0 {
			defaultArm = armIndex
			continue
		}
		for _, expr := range arm.Exprs {
			if typeCaseMatches(r, iv, expr) {
				selected = armIndex
				break
			}
		}
		if selected >= 0 {
			break
		}
	}
	if selected < 0 {
		selected = defaultArm
	}
	if selected < 0 {
		return
	}
	for armIndex := selected; armIndex < len(sw.Arms); armIndex++ {
		leaveArm := r.bashPPPushScope()
		if len(decl.Lhs) == 1 {
			name := decl.Lhs[0].Value
			vr := expand.Variable{Set: true, Kind: expand.String}
			if iv != nil && !iv.nilIface && iv.cell != nil {
				vr = iv.cell.vr
			}
			r.bashPPDeclareName(name, vr)
			target := r.bashPPScope.lookup(name)
			if target != nil && iv != nil && !iv.nilIface && iv.cell != nil {
				source := iv.cell
				target.typeName = source.typeName
				target.declType = source.declType
				target.pointer, target.nilPointer, target.pointerValue = source.pointer, source.nilPointer, source.pointerValue
				target.object, target.valueMeta = source.object, source.valueMeta
			}
		}
		r.stmts(ctx, sw.Arms[armIndex].Stmts)
		leaveArm()
		switch r.bashPPBranch {
		case bashPPBranchBreak:
			r.bashPPBranch = bashPPBranchNone
			r.exit.clear()
			return
		case bashPPBranchFallthrough:
			r.bashPPBranch = bashPPBranchNone
			r.exit.clear()
			continue
		default:
			return
		}
	}
}

func typeCaseMatches(r *Runner, iv *bashPPInterfaceValue, expr syntax.BashPPExpr) bool {
	id, ok := expr.(*syntax.BashPPIdent)
	if !ok {
		return false
	}
	if id.Name.Value == "nil" {
		return iv == nil || iv.nilIface
	}
	if iv == nil || iv.nilIface {
		return false
	}
	return r.bashPPTypeAssignable(iv.dynamic, &syntax.BashPPNamedType{Name: id.Name})
}
