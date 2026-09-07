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

type bashPPInterfaceMethod struct {
	spec *syntax.BashPPMethodSpec
	sig  string
}

type bashPPInterfaceMethods struct {
	byName map[string]bashPPInterfaceMethod
	order  []string
}

func (r *Runner) bashPPInterfaceType(typ syntax.BashPPTypeExpr) (*syntax.BashPPInterfaceType, bool) {
	seen := make(map[string]bool)
	for {
		if iface, ok := typ.(*syntax.BashPPInterfaceType); ok {
			return iface, true
		}
		named, ok := typ.(*syntax.BashPPNamedType)
		if !ok || named.Name == nil || seen[named.Name.Value] {
			return nil, false
		}
		seen[named.Name.Value] = true
		decl, found := r.bashPPTypes[named.Name.Value]
		if !found || decl.typeExpr == nil {
			return nil, false
		}
		typ = r.bashPPInstantiateNamedType(named)
	}
}

func (r *Runner) bashPPValidateInterfaceType(name string, iface *syntax.BashPPInterfaceType) error {
	_, err := r.bashPPInterfaceMethodSet(name, iface, make(map[string]bool))
	return err
}

func (r *Runner) bashPPInterfaceMethodSet(name string, iface *syntax.BashPPInterfaceType, stack map[string]bool) (*bashPPInterfaceMethods, error) {
	if stack[name] {
		return nil, fmt.Errorf("BASHPP-EINTERFACE-CYCLE: interface %s embeds itself", name)
	}
	stack[name] = true
	defer delete(stack, name)

	set := &bashPPInterfaceMethods{byName: make(map[string]bashPPInterfaceMethod)}
	direct := make(map[string]bool)
	for _, elem := range bashPPInterfaceElems(iface) {
		if elem.Method == nil {
			embeddedIface, ok := r.bashPPInterfaceType(elem.Embedded)
			if !ok {
				if bashPPDirectTypeSetTerm(elem.Embedded) {
					continue
				}
				return nil, fmt.Errorf("BASHPP-EINTERFACE-EMBED: interface %s embeds non-interface %s", name, bashPPTypeText(elem.Embedded))
			}
			embeddedName := bashPPTypeText(elem.Embedded)
			promoted, err := r.bashPPInterfaceMethodSet(embeddedName, embeddedIface, stack)
			if err != nil {
				return nil, err
			}
			for _, method := range promoted.order {
				candidate := promoted.byName[method]
				if existing, found := set.byName[method]; found && existing.sig != candidate.sig {
					return nil, fmt.Errorf("BASHPP-EINTERFACE-CONFLICT: interface %s has conflicting method %s", name, method)
				}
				if _, found := set.byName[method]; !found {
					set.order = append(set.order, method)
				}
				set.byName[method] = candidate
			}
			continue
		}
		spec := elem.Method
		if spec.Name == nil || !syntax.BashPPValidIdent(spec.Name.Value) {
			return nil, fmt.Errorf("BASHPP-EINTERFACE-METHOD: interface %s has invalid method name", name)
		}
		method := spec.Name.Value
		sig := bashPPMethodSpecSignature(spec)
		if direct[method] {
			return nil, fmt.Errorf("BASHPP-EINTERFACE-DUPLICATE: interface %s declares method %s more than once", name, spec.Name.Value)
		}
		direct[method] = true
		if existing, found := set.byName[method]; found && existing.sig != sig {
			return nil, fmt.Errorf("BASHPP-EINTERFACE-CONFLICT: interface %s has conflicting method %s", name, method)
		}
		if _, found := set.byName[method]; !found {
			set.order = append(set.order, method)
		}
		set.byName[method] = bashPPInterfaceMethod{spec: spec, sig: sig}
	}
	return set, nil
}

func bashPPDirectTypeSetTerm(typ syntax.BashPPTypeExpr) bool {
	switch typ.(type) {
	case *syntax.BashPPUnionType, *syntax.BashPPApproxType:
		return true
	}
	return false
}

func (r *Runner) bashPPInterfaceTypeSetSatisfied(arg syntax.BashPPTypeExpr, iface *syntax.BashPPInterfaceType, seen map[*syntax.BashPPInterfaceType]bool) bool {
	if iface == nil || seen[iface] {
		return iface != nil
	}
	seen[iface] = true
	defer delete(seen, iface)
	for _, elem := range bashPPInterfaceElems(iface) {
		if elem.Method != nil {
			continue
		}
		if embedded, ok := r.bashPPInterfaceType(elem.Embedded); ok {
			if !r.bashPPInterfaceTypeSetSatisfied(arg, embedded, seen) {
				return false
			}
			continue
		}
		if !r.bashPPTypeSetSatisfied(arg, elem.Embedded) {
			return false
		}
	}
	return true
}

func (r *Runner) bashPPInterfaceHasTypeTerms(iface *syntax.BashPPInterfaceType, seen map[*syntax.BashPPInterfaceType]bool) bool {
	if iface == nil || seen[iface] {
		return false
	}
	seen[iface] = true
	for _, elem := range bashPPInterfaceElems(iface) {
		if elem.Method != nil {
			continue
		}
		if embedded, ok := r.bashPPInterfaceType(elem.Embedded); ok {
			if r.bashPPInterfaceHasTypeTerms(embedded, seen) {
				return true
			}
			continue
		}
		if bashPPDirectTypeSetTerm(elem.Embedded) {
			return true
		}
	}
	return false
}

func bashPPInterfaceElems(iface *syntax.BashPPInterfaceType) []*syntax.BashPPInterfaceElem {
	if len(iface.Elems) > 0 {
		return iface.Elems
	}
	out := make([]*syntax.BashPPInterfaceElem, len(iface.Methods))
	for i, spec := range iface.Methods {
		out[i] = &syntax.BashPPInterfaceElem{Method: spec}
	}
	return out
}

func (r *Runner) bashPPImplements(actual syntax.BashPPTypeExpr, iface *syntax.BashPPInterfaceType) error {
	if actualIface, ok := r.bashPPInterfaceType(actual); ok {
		actualSet, err := r.bashPPInterfaceMethodSet(bashPPTypeText(actual), actualIface, make(map[string]bool))
		if err != nil {
			return err
		}
		expectedSet, err := r.bashPPInterfaceMethodSet("interface", iface, make(map[string]bool))
		if err != nil {
			return err
		}
		for _, name := range expectedSet.order {
			expected := expectedSet.byName[name]
			actualMethod, found := actualSet.byName[name]
			if !found {
				return fmt.Errorf("BASHPP-EINTERFACE-MISSING: %s does not implement interface (missing method %s)", bashPPTypeText(actual), name)
			}
			if actualMethod.sig != expected.sig {
				return fmt.Errorf("BASHPP-EINTERFACE-SIGNATURE: %s method %s has wrong signature", bashPPTypeText(actual), name)
			}
		}
		return nil
	}
	typeName, _ := bashPPInterfaceMethodOwner(actual)
	if typeName == "" {
		return fmt.Errorf("BASHPP-EINTERFACE-IMPOSSIBLE: %s cannot implement interface", bashPPTypeText(actual))
	}
	expectedSet, err := r.bashPPInterfaceMethodSet("interface", iface, make(map[string]bool))
	if err != nil {
		return err
	}
	for _, name := range expectedSet.order {
		expected := expectedSet.byName[name]
		sel := r.bashPPResolveSelection(actual, name, true, false)
		if sel.ambiguous || sel.method == nil && sel.interfaceSpec == nil {
			return fmt.Errorf("BASHPP-EINTERFACE-MISSING: %s does not implement interface (missing method %s)", bashPPTypeText(actual), name)
		}
		actualSig := ""
		if sel.method != nil {
			actualSig = bashPPInstantiatedMethodSignature(sel.method, sel.receiverType)
		} else {
			actualSig = bashPPMethodSpecSignature(sel.interfaceSpec)
		}
		if actualSig != expected.sig {
			return fmt.Errorf("BASHPP-EINTERFACE-SIGNATURE: %s method %s has wrong signature", bashPPTypeText(actual), name)
		}
	}
	return nil
}

func bashPPInstantiatedMethodSignature(fn *bashPPFunc, receiver syntax.BashPPTypeExpr) string {
	bindings := bashPPMethodTypeBindings(fn, receiver)
	return bashPPFieldsSignature(bashPPSubstituteFields(fn.decl.Params, bindings)) + "->" +
		bashPPFieldsSignature(bashPPSubstituteFields(fn.decl.Results, bindings))
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
	return bashPPFuncSignature(fn) == bashPPMethodSpecSignature(spec)
}

func bashPPFuncSignature(fn *syntax.BashPPFuncDecl) string {
	return bashPPFieldsSignature(fn.Params) + "->" + bashPPFieldsSignature(fn.Results)
}

func bashPPMethodSpecSignature(spec *syntax.BashPPMethodSpec) string {
	return bashPPFieldsSignature(spec.Params) + "->" + bashPPFieldsSignature(spec.Results)
}

func bashPPFieldsSignature(fields []*syntax.BashPPField) string {
	var b strings.Builder
	for i, field := range fields {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for j := 0; j < count; j++ {
			if i > 0 || j > 0 {
				b.WriteByte(',')
			}
			if field.Variadic() {
				b.WriteString("...")
			}
			if field.FieldType != nil {
				b.WriteString(field.FieldType.Value)
			}
		}
	}
	return b.String()
}

func (r *Runner) bashPPMakeInterfaceValue(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (*bashPPInterfaceValue, expand.Variable, error) {
	if id, ok := expr.(*syntax.BashPPIdent); ok && id.Name.Value == "nil" {
		return &bashPPInterfaceValue{nilIface: true}, expand.Variable{Set: true, Kind: expand.String}, nil
	}
	iface, _ := r.bashPPInterfaceType(expected)
	if iface == nil {
		return nil, expand.Variable{}, fmt.Errorf("BASHPP-EINTERFACE-TYPE: %s is not an interface", bashPPTypeText(expected))
	}
	if r.bashPPInterfaceHasTypeTerms(iface, make(map[*syntax.BashPPInterfaceType]bool)) {
		return nil, expand.Variable{}, fmt.Errorf("BASHPP-EINTERFACE-TYPESET: %s is a constraint interface and cannot be used as a value type", bashPPTypeText(expected))
	}
	if id, ok := expr.(*syntax.BashPPIdent); ok {
		if source := r.bashPPScope.lookup(id.Name.Value); source != nil && source.interfaceValue != nil {
			if err := r.bashPPImplements(source.declType, iface); err != nil {
				return nil, expand.Variable{}, err
			}
			if source.interfaceValue.nilIface {
				return &bashPPInterfaceValue{nilIface: true}, expand.Variable{Set: true, Kind: expand.String}, nil
			}
			iv := *source.interfaceValue
			iv.cell = bashPPCopyInterfaceCell(source.interfaceValue.cell)
			return &iv, iv.cell.vr, nil
		}
	}
	if _, ok := expr.(*syntax.BashPPSelectorExpr); ok {
		_, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return nil, expand.Variable{}, err
		}
		if meta == nil || meta.interfaceValue == nil {
			return nil, expand.Variable{}, fmt.Errorf("BASHPP-EINTERFACE-VALUE: selector is not an interface value")
		}
		source := meta.interfaceValue
		if source.nilIface {
			return &bashPPInterfaceValue{nilIface: true}, expand.Variable{Set: true, Kind: expand.String}, nil
		}
		if err := r.bashPPImplements(source.dynamic, iface); err != nil {
			return nil, expand.Variable{}, err
		}
		iv := *source
		iv.cell = bashPPCopyInterfaceCell(source.cell)
		return &iv, iv.cell.vr, nil
	}
	cell, actual, err := r.bashPPCellForInterfaceExpr(expr)
	if err != nil {
		return nil, expand.Variable{}, err
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
				return cell, cell.declType, nil
			}
			return cell.interfaceValue.cell, cell.interfaceValue.dynamic, nil
		}
		actual := cell.declType
		if actual == nil {
			if meta := bashPPCellMeta(cell); meta != nil {
				actual = meta.typ
			}
		}
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
	if iface, ok := r.bashPPInterfaceType(cell.declType); ok {
		if _, assertIface := r.bashPPInterfaceType(assert.Assert); !assertIface {
			if err := r.bashPPImplements(assert.Assert, iface); err != nil {
				return nil, nil, fmt.Errorf("BASHPP-EASSERT-IMPOSSIBLE: %s cannot be asserted from %s", bashPPTypeText(assert.Assert), bashPPTypeText(cell.declType))
			}
		}
	}
	assertIface, assertingInterface := r.bashPPInterfaceType(assert.Assert)
	matched := false
	if !iv.nilIface {
		if assertingInterface {
			matched = r.bashPPImplements(iv.dynamic, assertIface) == nil
		} else {
			matched = bashPPTypeText(iv.dynamic) == bashPPTypeText(assert.Assert)
		}
	}
	if !matched {
		if commaOK {
			if assertingInterface {
				zero := &bashPPCell{declType: assert.Assert, interfaceValue: &bashPPInterfaceValue{nilIface: true}}
				zero.vr = expand.Variable{Set: true, Kind: expand.String}
				return []string{"", "false"}, zero, nil
			}
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
	if assertingInterface {
		source := &bashPPCell{declType: assert.Assert, interfaceValue: &bashPPInterfaceValue{
			dynamic: iv.dynamic,
			cell:    bashPPCopyInterfaceCell(iv.cell),
		}}
		source.vr = source.interfaceValue.cell.vr
		return []string{source.vr.Str, "true"}, source, nil
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
	target := &syntax.BashPPNamedType{Name: id.Name}
	if iface, ok := r.bashPPInterfaceType(target); ok {
		return r.bashPPImplements(iv.dynamic, iface) == nil
	}
	return r.bashPPTypeAssignable(iv.dynamic, target)
}
