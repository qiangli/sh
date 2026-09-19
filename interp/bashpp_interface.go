// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"go/constant"
	"go/types"
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
	// pkg is the linked-package tag of the declared interface the method
	// is spelled in; see gosource_method_package.go. An unexported method
	// name is only the same method in the same package.
	pkg string
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
		// Imported interfaces are not declarations in the interpreter's type
		// registry. Resolve them through the authenticated export metadata that
		// the native bridge already collected; concrete imported types must not
		// take this path.
		if iface, ok := r.bashPPImportedInterfaceType(named); ok {
			return iface, true
		}
		decl, found := r.bashPPTypes[named.Name.Value]
		if !found && named.Name.Value == "any" {
			return &syntax.BashPPInterfaceType{Interface: &syntax.Lit{Value: "interface"}}, true
		}
		if !found && named.Name.Value == "error" {
			return bashPPPredeclaredErrorInterface(), true
		}
		if !found && named.Name.Value == "runtime.Error" && r.bashPPGoSource {
			return bashPPRuntimeErrorInterface(), true
		}
		if !found || decl.typeExpr == nil {
			return nil, false
		}
		typ = r.bashPPInstantiateNamedType(named)
	}
}

// bashPPImportedInterfaceType exposes only the method set of an imported
// interface. Its source identity stays in nativeTypes, which is populated
// from go/types export data and is therefore not forgeable by a display name.
// The narrow projection is intentional: imported concrete types are still
// native values, not interface declarations.
func (r *Runner) bashPPImportedInterfaceType(named *syntax.BashPPNamedType) (*syntax.BashPPInterfaceType, bool) {
	if !r.bashPPGoSource || named == nil || named.Name == nil {
		return nil, false
	}
	native := r.bashPPEmbeddedNativeType(named)
	if native == nil {
		return nil, false
	}
	iface, ok := types.Unalias(native).Underlying().(*types.Interface)
	if !ok || !iface.IsMethodSet() {
		return nil, false
	}
	iface.Complete()
	out := &syntax.BashPPInterfaceType{Interface: &syntax.Lit{Value: "interface"}}
	for i := 0; i < iface.NumMethods(); i++ {
		method := iface.Method(i)
		sig, ok := method.Type().(*types.Signature)
		if !ok || sig.TypeParams().Len() != 0 {
			return nil, false
		}
		out.Methods = append(out.Methods, bashPPImportedMethodSpec(method.Name(), sig))
	}
	return out, true
}

func bashPPImportedMethodSpec(name string, sig *types.Signature) *syntax.BashPPMethodSpec {
	spec := &syntax.BashPPMethodSpec{Name: &syntax.Lit{Value: name}}
	qualifier := func(pkg *types.Package) string {
		if pkg == nil {
			return ""
		}
		return pkg.Name()
	}
	fields := func(tuple *types.Tuple, variadic bool) []*syntax.BashPPField {
		if tuple == nil {
			return nil
		}
		out := make([]*syntax.BashPPField, 0, tuple.Len())
		for i := 0; i < tuple.Len(); i++ {
			typ := tuple.At(i).Type()
			field := &syntax.BashPPField{FieldTypeExpr: syntax.BashPPTypeExprFromText(types.TypeString(typ, qualifier))}
			if variadic && i == tuple.Len()-1 {
				if slice, ok := typ.(*types.Slice); ok {
					field.FieldTypeExpr = syntax.BashPPTypeExprFromText(types.TypeString(slice.Elem(), qualifier))
					field.Ellipsis = syntax.NewPos(0, 1, 1)
				}
			}
			out = append(out, field)
		}
		return out
	}
	spec.Params = fields(sig.Params(), sig.Variadic())
	spec.Results = fields(sig.Results(), false)
	return spec
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
	pkg := r.goSourceInterfacePackage(iface)
	for _, elem := range bashPPInterfaceElems(iface) {
		if elem.Method == nil {
			embeddedIface, ok := r.bashPPInterfaceType(elem.Embedded)
			if !ok {
				if bashPPDirectTypeSetTerm(elem.Embedded) || r.bashPPSingleTypeTerm(elem.Embedded) {
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
		set.byName[method] = bashPPInterfaceMethod{spec: spec, sig: sig, pkg: pkg}
	}
	return set, nil
}

// bashPPSingleTypeTerm reports whether a non-interface element embedded in
// an interface is a one-term type set — `interface{ string }`, `interface{
// []byte }`, `interface{ *B }`, `interface{ List[T] }` — which contributes
// no methods and restricts the constraint's type set exactly as the union
// spelling of the same term would. A bare name that resolves to nothing is
// not one: it is an interface the runtime does not know, and saying so is
// better than silently narrowing the type set to a type that does not exist.
func (r *Runner) bashPPSingleTypeTerm(typ syntax.BashPPTypeExpr) bool {
	switch t := typ.(type) {
	case *syntax.BashPPNamedType:
		if t.Name == nil {
			return false
		}
		if _, declared := r.bashPPTypes[t.Name.Value]; declared {
			return true
		}
		return bashPPBuiltinType(t.Name.Value)
	case *syntax.BashPPCollectionType, *syntax.BashPPPointerType, *syntax.BashPPFuncType, *syntax.BashPPChanType, *syntax.BashPPStructType, *syntax.BashPPTypeParamType:
		return true
	}
	return false
}

func bashPPDirectTypeSetTerm(typ syntax.BashPPTypeExpr) bool {
	switch t := typ.(type) {
	case *syntax.BashPPNamedType:
		return t.Name.Value == "comparable"
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
	// An interface with no methods — `any`, or a constraint made only of
	// type terms such as `interface{ []int64 | [5]int64 }` — is implemented
	// by every type, including one that has no method owner at all; its
	// type terms are the caller's separate check.
	if methods, err := r.bashPPInterfaceMethodSet("interface", iface, make(map[string]bool)); err == nil && len(methods.order) == 0 {
		return nil
	}
	if bashPPRuntimeErrorType(actual) {
		return r.bashPPRuntimeErrorImplements(actual, iface)
	}
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
			if !found || goSourceUnexportedName(name) && actualMethod.pkg != expected.pkg {
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
		if goSourceUnexportedName(name) && r.goSourceMethodPackage(sel) != expected.pkg {
			return fmt.Errorf("BASHPP-EINTERFACE-MISSING: %s does not implement interface (missing method %s)", bashPPTypeText(actual), name)
		}
		actualSig := ""
		if sel.method != nil {
			// Go 1.27 gives methods their own type parameters but does NOT
			// give them to interfaces: an interface method may declare none,
			// so a generic method is not in the method set an interface can
			// name. Reporting it as a distinct error rather than a signature
			// mismatch keeps the reason visible.
			if sel.method.decl != nil && len(sel.method.decl.TypeParams) > 0 {
				return fmt.Errorf("BASHPP-EINTERFACE-GENERIC: %s method %s declares type parameters and cannot implement an interface method", bashPPTypeText(actual), name)
			}
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
			// The type tree is the canonical spelling: a substituted field
			// is respelled from its tree, so a signature written `func(T)
			// bool` and one instantiated to `func(int)(bool)` must both be
			// read from the tree to compare equal.
			if field.FieldTypeExpr != nil {
				b.WriteString(bashPPTypeText(field.FieldTypeExpr))
			} else if field.FieldType != nil {
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
	if cell, handled, err := r.goSourceNilValueCell(expr); handled {
		if err != nil {
			return nil, expand.Variable{}, err
		}
		if cell.interfaceValue != nil && cell.interfaceValue.nilIface {
			return &bashPPInterfaceValue{nilIface: true}, cell.vr, nil
		}
		if err := r.bashPPImplements(cell.declType, iface); err != nil {
			return nil, expand.Variable{}, err
		}
		return &bashPPInterfaceValue{dynamic: cell.declType, cell: cell}, cell.vr, nil
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
	if iv, vr, handled, err := r.goSourceConvertedInterfaceValue(expr, iface); handled {
		return iv, vr, err
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

// bashPPInterfaceAssignCandidate builds the assignment candidate for a target
// declared with an interface type. The dynamic value and the interface value
// naming its type are captured together, so `i = &T{…}` and `i = 42` store
// what Go stores instead of being read as a bare scalar with no dynamic type.
// It reports whether it claimed the assignment at all.
func (r *Runner) bashPPInterfaceAssignCandidate(target *bashPPCell, expr syntax.BashPPExpr) (*bashPPCell, bool, error) {
	if target == nil || target.declType == nil || expr == nil {
		return nil, false, nil
	}
	if _, ok := r.bashPPInterfaceType(target.declType); !ok {
		return nil, false, nil
	}
	iv, vr, err := r.bashPPMakeInterfaceValue(expr, target.declType)
	if err != nil {
		return nil, true, err
	}
	cell := &bashPPCell{vr: vr, declType: target.declType, interfaceValue: iv}
	if iv != nil && iv.cell != nil {
		cell.valueMeta, cell.object, cell.scalarKind = iv.cell.valueMeta, iv.cell.object, iv.cell.scalarKind
	}
	return cell, true, nil
}

// bashPPAssertCandidate settles `x = i.(T)` — a one-result type assertion used
// as an assignment's right-hand side. It reports whether it claimed the
// expression. A failed one-result assertion is a language-level panic in Go,
// which the caller keeps by making the failure fatal.
func (r *Runner) bashPPAssertCandidate(expr syntax.BashPPExpr) (*bashPPCell, bool, error) {
	assert, ok := expr.(*syntax.BashPPTypeAssertExpr)
	if !ok {
		return nil, false, nil
	}
	if assert.TypeToken != nil {
		return nil, true, fmt.Errorf("BASHPP-EASSERT-TYPE: .(type) is only valid in a type switch")
	}
	values, source, err := r.bashPPTypeAssert(assert, false)
	if err != nil {
		return nil, true, err
	}
	if source == nil {
		return &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: values[0]}}, true, nil
	}
	candidate := *source
	return &candidate, true, nil
}

// bashPPBindInterfaceParam gives a parameter declared with an interface type
// the interface value Go's assignment to it would have produced: the argument
// keeps its own dynamic type, which is what a type switch or an assertion in
// the body reads. An argument that already arrived as an interface value keeps
// the dynamic type it was carrying.
func (r *Runner) bashPPBindInterfaceParam(cell *bashPPCell, typ syntax.BashPPTypeExpr) error {
	if cell == nil || typ == nil || cell.interfaceValue != nil {
		return nil
	}
	iface, ok := r.bashPPInterfaceType(typ)
	if !ok {
		return nil
	}
	dynamic := cell.declType
	if dynamic == nil {
		if meta := bashPPCellMeta(cell); meta != nil {
			dynamic = meta.typ
		}
	}
	if dynamic == nil && cell.typeName != "" {
		dynamic, _ = bashPPScalarNamedType(cell.typeName)
	}
	if dynamic == nil {
		dynamic = r.bashPPFuncValueType(cell)
	}
	if dynamic == nil {
		if name := bashPPDefaultScalarTypeName(cell.scalarKind); name != "" {
			dynamic = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: name}}
		}
	}
	// Without a dynamic type there is nothing an interface could record, and
	// a value already typed as the interface itself is not its own dynamic
	// type; both are left exactly as they arrived.
	if dynamic == nil || bashPPTypeText(dynamic) == bashPPTypeText(typ) {
		return nil
	}
	if err := r.bashPPImplements(dynamic, iface); err != nil {
		return err
	}
	cell.interfaceValue = &bashPPInterfaceValue{dynamic: dynamic, cell: bashPPCopyInterfaceCell(cell)}
	return nil
}

// bashPPInterfaceConversion evaluates a conversion to an interface type —
// `any(x)`, `interface{}(d)`, `I(v)` — to a cell holding the interface value,
// which is how a type switch or assertion reads a type parameter's dynamic
// type. It reports false for any other expression. The operand is read the
// way an interface assignment reads its source, so a struct is captured by
// value and an interface operand contributes its own dynamic value.
func (r *Runner) bashPPInterfaceConversion(x syntax.BashPPExpr) (*bashPPCell, bool, error) {
	if paren, ok := x.(*syntax.BashPPParenExpr); ok {
		return r.bashPPInterfaceConversion(paren.X)
	}
	conv, ok := x.(*syntax.BashPPConvertExpr)
	if !ok {
		return nil, false, nil
	}
	target := r.bashPPBindTypeExpr(r.bashPPConvertTarget(conv))
	if target == nil {
		return nil, false, nil
	}
	iface, ok := r.bashPPInterfaceType(target)
	if !ok {
		return nil, false, nil
	}
	if source, handled, err := r.goSourceNilValueCell(conv.X); handled {
		if err != nil {
			return nil, true, err
		}
		cell := &bashPPCell{declType: target, vr: expand.Variable{Set: true, Kind: expand.String}}
		if source.interfaceValue != nil && source.interfaceValue.nilIface {
			cell.interfaceValue = &bashPPInterfaceValue{nilIface: true}
			return cell, true, nil
		}
		if err := r.bashPPImplements(source.declType, iface); err != nil {
			return nil, true, err
		}
		cell.interfaceValue = &bashPPInterfaceValue{dynamic: source.declType, cell: bashPPCopyInterfaceCell(source)}
		cell.vr = cell.interfaceValue.cell.vr
		return cell, true, nil
	}
	source, dynamic, err := r.bashPPCellForInterfaceExpr(conv.X)
	if err != nil {
		return nil, true, err
	}
	cell := &bashPPCell{declType: target, vr: expand.Variable{Set: true, Kind: expand.String}}
	if source.interfaceValue != nil && source.interfaceValue.nilIface {
		cell.interfaceValue = &bashPPInterfaceValue{nilIface: true}
		return cell, true, nil
	}
	if err := r.bashPPImplements(dynamic, iface); err != nil {
		return nil, true, err
	}
	cell.interfaceValue = &bashPPInterfaceValue{dynamic: dynamic, cell: bashPPCopyInterfaceCell(source)}
	cell.vr = cell.interfaceValue.cell.vr
	return cell, true, nil
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
	if cell, ok := r.goSourceNilInterfaceSource(expr); ok {
		return cell, nil, nil
	}
	// `true` and `false` are identifiers, not literals; when no variable
	// shadows them they are the untyped boolean constants the scalar
	// evaluator below already knows, and store as a bool.
	if id, ok := expr.(*syntax.BashPPIdent); ok && !(bashPPBoolIdent(id.Name.Value) && r.bashPPScope.lookup(id.Name.Value) == nil) {
		cell := r.bashPPScope.lookup(id.Name.Value)
		if cell == nil {
			return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: undefined value %s", id.Name.Value)
		}
		// An untyped constant's name stores what its literal would: the
		// value with the constant's default type (`const a = 0; var i any =
		// a` holds an int). The constant cell carries no type of its own,
		// so the scalar path names the default from the value's kind.
		if cell.constant && cell.declType == nil && cell.typeName == "" && cell.interfaceValue == nil && cell.vr.Kind == expand.String {
			return r.bashPPScalarInterfaceCell(expr)
		}
		return r.bashPPInterfaceSourceCell(cell, id.Name.Value)
	}
	// A call's result is the cell the callee returned: an interface result
	// contributes its own dynamic value, a typed one its declared type. Read
	// as a scalar it would keep only a type NAME, which for `List[int]` is
	// not a type at all.
	if call, ok := expr.(*syntax.BashPPCall); ok && r.bashPPGoSource {
		cell, err := r.goSourceValueCell(call)
		if err != nil {
			return nil, nil, err
		}
		if cell.vr.Kind != expand.Object && cell.interfaceValue == nil && cell.declType == nil && !cell.pointer {
			// A plain scalar result: the scalar path below names its default
			// type and is what every other scalar takes.
			return r.bashPPScalarInterfaceCell(expr)
		}
		return r.bashPPInterfaceSourceCell(cell, "call result")
	}
	// A dynamic value need not be a variable. `var i I = T{"hello"}`,
	// `i = &T{}` and `i = 42` all store a value the interface then owns, so
	// each is materialized into an anonymous cell carrying the dynamic type
	// the assignment's method-set check is made against.
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.bashPPCellForInterfaceExpr(x.X)
	case *syntax.BashPPFuncLit:
		// `x = func() {…}`, or a generic function value the front end lowers
		// to a literal: the interface owns the closure, whose dynamic type
		// is the literal's own signature.
		cell, handled, err := r.goSourceCallableCell(x)
		if err != nil || !handled {
			return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: function literal cannot be stored")
		}
		return cell, cell.declType, nil
	case *syntax.BashPPCompositeLit:
		if x.LitType == nil {
			return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: composite literal has no type")
		}
		value, meta, err := r.bashPPEvalComposite(x, nil)
		if err != nil {
			return nil, nil, err
		}
		cell := &bashPPCell{declType: x.LitType}
		if named, ok := x.LitType.(*syntax.BashPPNamedType); ok && named.Name != nil {
			cell.typeName = named.Name.Value
		}
		bashPPStoreCellValue(cell, value, meta)
		return cell, x.LitType, nil
	case *syntax.BashPPAddressExpr, *syntax.BashPPNewExpr:
		ptr, err := r.bashPPPointerExprValue(expr)
		if err != nil {
			return nil, nil, err
		}
		cell := bashPPPointerCell(ptr)
		if cell.declType == nil {
			return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: pointer value has no dynamic type")
		}
		return cell, cell.declType, nil
	case *syntax.BashPPConvertExpr:
		// `[]byte("0")` stored in an interface is the converted slice with
		// the conversion's own type, not a scalar reading of its spelling.
		if cell, handled, err := r.bashPPConvertCollectionCell(x); handled {
			if err != nil {
				return nil, nil, err
			}
			return cell, cell.declType, nil
		}
	}
	return r.bashPPScalarInterfaceCell(expr)
}

// bashPPScalarInterfaceCell materializes a scalar expression as the cell an
// interface stores, typed by its named type or its untyped constant's Go
// default type.
func (r *Runner) bashPPScalarInterfaceCell(expr syntax.BashPPExpr) (*bashPPCell, syntax.BashPPTypeExpr, error) {
	value, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		return nil, nil, err
	}
	name := value.typ
	if name == "" {
		name = bashPPDefaultScalarTypeName(value.value.Kind())
	}
	if name == "" {
		return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: interface assignment requires a named value")
	}
	actual, name := bashPPScalarNamedType(name)
	cell := &bashPPCell{
		vr:         expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(value.value)},
		scalarKind: value.value.Kind(),
		typeName:   name,
		declType:   actual,
	}
	return cell, actual, nil
}

// bashPPInterfaceSourceCell reads an existing cell as an interface source:
// an interface value contributes its dynamic value, anything else is its own
// value with the type it declares or carries.
func (r *Runner) bashPPInterfaceSourceCell(cell *bashPPCell, what string) (*bashPPCell, syntax.BashPPTypeExpr, error) {
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
		actual, _ = bashPPScalarNamedType(cell.typeName)
	}
	// A closure bound by `:=` carries only its handle; its dynamic type is
	// the signature of the literal it was made from.
	if actual == nil && cell.vr.Kind == expand.String {
		if fn, ok := r.bashPPClosure(cell.vr.Str); ok && fn.lit != nil {
			actual = bashPPClosureType(fn)
		}
	}
	if actual == nil {
		return nil, nil, fmt.Errorf("BASHPP-EINTERFACE-VALUE: %s has no dynamic type", what)
	}
	return cell, actual, nil
}

func bashPPBoolIdent(name string) bool {
	return name == "true" || name == "false"
}

// bashPPDefaultScalarTypeName names the Go default type of an untyped
// constant, which is the dynamic type it takes on when stored in an interface.
func bashPPDefaultScalarTypeName(kind constant.Kind) string {
	switch kind {
	case constant.Bool:
		return "bool"
	case constant.String:
		return "string"
	case constant.Int:
		return "int"
	case constant.Float:
		return "float64"
	}
	return ""
}

func (r *Runner) bashPPTypeAssert(assert *syntax.BashPPTypeAssertExpr, commaOK bool) ([]string, *bashPPCell, error) {
	cell, err := r.bashPPInterfaceOperand(assert.X, "type assertion")
	if err != nil {
		return nil, nil, err
	}
	return r.bashPPTypeAssertCell(assert, commaOK, cell)
}

// bashPPInterfaceOperand evaluates the operand of a type assertion or type
// switch to the cell holding its interface value. A name is its own cell;
// any other expression — `any(x)`, `interface{}(d)`, a call, a field — is
// read and must carry an interface value. Only a nil cell is reported here;
// a non-interface name is the caller's message.
func (r *Runner) bashPPInterfaceOperand(x syntax.BashPPExpr, what string) (*bashPPCell, error) {
	if r.bashPPGoSource && bashPPRecoverExpr(x) && r.bashPPFuncs["recover"] == nil && (r.bashPPScope == nil || r.bashPPScope.lookup("recover") == nil) {
		iv, _ := r.bashPPRecoverInterfaceValue()
		cell := &bashPPCell{declType: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "any"}}, interfaceValue: iv}
		if iv.cell != nil {
			cell.vr = iv.cell.vr
		}
		return cell, nil
	}
	if id, ok := x.(*syntax.BashPPIdent); ok {
		return r.bashPPScope.lookup(id.Name.Value), nil
	}
	if cell, ok, err := r.bashPPInterfaceConversion(x); ok {
		return cell, err
	}
	// A call's interface result is its result cell, which already carries
	// the dynamic value the callee returned.
	if call, ok := x.(*syntax.BashPPCall); ok && r.bashPPGoSource {
		cell, err := r.goSourceValueCell(call)
		if err != nil {
			return nil, err
		}
		if cell == nil || cell.interfaceValue == nil {
			return nil, fmt.Errorf("BASHPP-EASSERT-OPERAND: %s operand must be an interface", what)
		}
		return cell, nil
	}
	value, meta, err := r.bashPPReadExpr(x)
	if err != nil {
		return nil, err
	}
	if meta == nil || meta.interfaceValue == nil {
		return nil, fmt.Errorf("BASHPP-EASSERT-OPERAND: %s operand must be an interface", what)
	}
	cell := &bashPPCell{declType: meta.typ, interfaceValue: meta.interfaceValue}
	if meta.interfaceValue.nilIface {
		cell.vr = expand.Variable{Set: true, Kind: expand.String}
	} else if meta.interfaceValue.cell != nil {
		cell.vr = meta.interfaceValue.cell.vr
	} else {
		cell.vr = expand.NewObject(value)
	}
	return cell, nil
}

func (r *Runner) bashPPTypeAssertCell(assert *syntax.BashPPTypeAssertExpr, commaOK bool, cell *bashPPCell) ([]string, *bashPPCell, error) {
	if cell == nil || cell.interfaceValue == nil {
		return nil, nil, fmt.Errorf("BASHPP-EASSERT-OPERAND: type assertion operand is not an interface")
	}
	iv := cell.interfaceValue
	// The static impossibility check is the classic dialect's own guard. A
	// Go-source program was already vetted by go/types, which rejects every
	// genuinely impossible assertion at parse time; the only asserted
	// spellings that reach here despite not implementing the interface are
	// instantiated type parameters (`x.(T)` with T a non-implementing
	// instantiation), which Go resolves dynamically — comma-ok yields false
	// and the bare form panics. See typeparam/issue50002.go.
	if iface, ok := r.bashPPInterfaceType(cell.declType); ok && !r.bashPPGoSource {
		if _, assertIface := r.bashPPInterfaceType(assert.Assert); !assertIface && !r.goSourceImportedTypeName(bashPPTypeText(assert.Assert)) {
			methods, methodErr := r.bashPPInterfaceMethodSet("interface", iface, make(map[string]bool))
			if methodErr != nil {
				return nil, nil, methodErr
			}
			if len(methods.order) > 0 && r.bashPPImplements(assert.Assert, iface) != nil {
				return nil, nil, fmt.Errorf("BASHPP-EASSERT-IMPOSSIBLE: %s cannot be asserted from %s", bashPPTypeText(assert.Assert), bashPPTypeText(cell.declType))
			}
		}
	}
	assertIface, assertingInterface := r.bashPPInterfaceType(assert.Assert)
	matched := false
	if !iv.nilIface {
		// A dependency-owned dynamic value asserted to an interface — local
		// or imported — answers from the dependency; see
		// bashpp_sprint165_runtime_panic.go.
		native, claimed, err := r.goSourceNativeAssertsInterface(iv, assert.Assert)
		if err != nil {
			return nil, nil, err
		}
		if claimed {
			matched, assertingInterface = native, true
		} else if assertingInterface {
			matched = r.bashPPImplements(iv.dynamic, assertIface) == nil
		} else {
			matched = bashPPInterfaceAssertTypeText(r.bashPPPredeclaredAliases(iv.dynamic)) == bashPPInterfaceAssertTypeText(r.bashPPPredeclaredAliases(assert.Assert)) ||
				r.goSourceNativeTypeIdentical(iv.dynamic, assert.Assert)
			// A struct literal type is identified by its fields, not by the
			// word "struct"; see gosource_struct_identity.go.
			if matched && (bashPPStructLiteralType(iv.dynamic) || bashPPStructLiteralType(assert.Assert)) {
				matched = r.goSourceDynamicTypeIdentity(iv.dynamic) == r.goSourceDynamicTypeIdentity(assert.Assert)
			}
			// Same spelling, possibly different declarations: a type declared
			// inside a function is its own type; see bashpp_sprint162_type_scope.go.
			if matched {
				matched = r.goSourceSameTypeScope(iv.dynamic, assert.Assert)
			}
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
		if r.bashPPGoSource {
			return nil, nil, r.bashPPRaiseRuntimeError(bashPPRuntimeTypeAssert, r.goSourceTypeAssertionFailure(cell.declType, iv, assert.Assert, assertIface))
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

func bashPPInterfaceAssertTypeText(typ syntax.BashPPTypeExpr) string {
	text := bashPPGoCanonicalTypeText(typ)
	text = strings.ReplaceAll(text, "interface{}", "any")
	text = strings.ReplaceAll(text, "interface {}", "any")
	text = strings.ReplaceAll(text, "interface", "any")
	return text
}

// bashPPGoCanonicalTypeText is bashPPTypeText except a function type is spelled
// the way Go's own reflect/types renderers do: parameters joined by ", ", a
// single unnamed result written bare, several results parenthesized, and no
// trailing "()" when there are none. A dependency reports a function value's
// dynamic type in that Go spelling (`func(string)`, `func(io.Writer, string)
// (int, error)`), so a BashPPFuncType parsed from an asserted spelling must
// render the same way here; the interpreter's internal `func(p)(r)` form would
// make an exact-match assertion against a native func handle fail spuriously.
func bashPPGoCanonicalTypeText(typ syntax.BashPPTypeExpr) string {
	ft, ok := typ.(*syntax.BashPPFuncType)
	if !ok {
		return bashPPTypeText(typ)
	}
	params := bashPPGoCanonicalFieldTypes(ft.Params)
	text := "func(" + strings.Join(params, ", ") + ")"
	results := bashPPGoCanonicalFieldTypes(ft.Results)
	switch len(results) {
	case 0:
	case 1:
		text += " " + results[0]
	default:
		text += " (" + strings.Join(results, ", ") + ")"
	}
	return text
}

// bashPPGoCanonicalFieldTypes expands a parameter/result list into one type
// spelling per value: a group `(a, b int)` contributes two `int`s, a variadic
// group keeps its leading `...`, and every nested function type is spelled in
// the same Go-canonical form.
func bashPPGoCanonicalFieldTypes(fields []*syntax.BashPPField) []string {
	var out []string
	for _, field := range fields {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		text := ""
		if field.FieldTypeExpr != nil {
			text = bashPPGoCanonicalTypeText(field.FieldTypeExpr)
		} else if field.FieldType != nil {
			text = field.FieldType.Value
		}
		if field.Variadic() {
			text = "..." + text
		}
		for j := 0; j < count; j++ {
			out = append(out, text)
		}
	}
	return out
}

func (r *Runner) bashPPTypeSwitch(ctx context.Context, sw *syntax.BashPPSwitch) {
	decl, _ := sw.Init.(*syntax.BashPPShortDecl)
	assert, _ := decl.Expr.(*syntax.BashPPTypeAssertExpr)
	cell, err := r.bashPPInterfaceOperand(assert.X, "type switch")
	if err != nil {
		r.errf("BASHPP-ETYPESWITCH-OPERAND: %v\n", strings.TrimPrefix(err.Error(), "BASHPP-EASSERT-OPERAND: "))
		r.exit = exitStatus{code: 2}
		return
	}
	if cell == nil || cell.interfaceValue == nil {
		r.errf("BASHPP-ETYPESWITCH-OPERAND: %s is not an interface\n", bashPPExprText(assert.X))
		r.exit = exitStatus{code: 2}
		return
	}
	iv := cell.interfaceValue
	selected, defaultArm := -1, -1
	for armIndex, arm := range sw.Arms {
		if len(arm.Types) == 0 && len(arm.Exprs) == 0 {
			defaultArm = armIndex
			continue
		}
		for _, typ := range arm.Types {
			if typeCaseTypeMatches(r, iv, typ) {
				selected = armIndex
				break
			}
		}
		if selected >= 0 {
			break
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
			if r.bashPPBranchEscapesEligible() {
				return
			}
			r.bashPPClearBranch()
			return
		case bashPPBranchFallthrough:
			r.bashPPClearBranch()
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
	return typeCaseTypeMatches(r, iv, &syntax.BashPPNamedType{Name: id.Name})
}

func typeCaseTypeMatches(r *Runner, iv *bashPPInterfaceValue, target syntax.BashPPTypeExpr) bool {
	if named, ok := target.(*syntax.BashPPNamedType); ok && named.Name.Value == "nil" {
		return iv == nil || iv.nilIface
	}
	if iv == nil || iv.nilIface {
		return false
	}
	// A dependency-owned dynamic value against an interface case — local or
	// imported — answers from the dependency; see
	// bashpp_sprint165_runtime_panic.go. A refusal there is a diagnostic the
	// switch statement reports, not a case that silently misses.
	native, claimed, err := r.goSourceNativeAssertsInterface(iv, target)
	if err != nil {
		r.exit.fatal(err)
		return false
	}
	if claimed {
		return native
	}
	if iface, ok := r.bashPPInterfaceType(target); ok {
		return r.bashPPImplements(iv.dynamic, iface) == nil
	}
	if bashPPStructLiteralType(iv.dynamic) || bashPPStructLiteralType(target) {
		return r.goSourceDynamicTypeIdentity(iv.dynamic) == r.goSourceDynamicTypeIdentity(target)
	}
	return r.bashPPTypeAssignable(iv.dynamic, target) && r.goSourceSameTypeScope(iv.dynamic, target)
}

// bashPPStructLiteralType reports whether a type is spelled as a struct
// literal rather than by a name.
func bashPPStructLiteralType(typ syntax.BashPPTypeExpr) bool {
	_, ok := typ.(*syntax.BashPPStructType)
	return ok
}

// The predeclared error type is an ordinary interface. A source declaration
// named error takes precedence through the normal named-type lookup above.
func bashPPPredeclaredErrorInterface() *syntax.BashPPInterfaceType {
	result := &syntax.BashPPField{FieldType: &syntax.Lit{Value: "string"}, FieldTypeExpr: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}}
	method := &syntax.BashPPMethodSpec{Name: &syntax.Lit{Value: "Error"}, Results: []*syntax.BashPPField{result}}
	return &syntax.BashPPInterfaceType{Interface: &syntax.Lit{Value: "interface"}, Elems: []*syntax.BashPPInterfaceElem{{Method: method}}}
}
