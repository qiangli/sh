// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"errors"
	"fmt"
	"go/constant"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

var errBashPPTypeCycle = errors.New("cyclic type representation")

func cloneBashPPTypeSet(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for key := range src {
		dst[key] = true
	}
	return dst
}

// bashPPValidateTypeRepresentation follows instantiated named types while
// retaining two pieces of state: all active instantiations (to terminate legal
// recursive paths), and the active instantiations since the last representation
// boundary. Re-entering the latter is an infinite value layout. Pointers,
// slices, and maps reset that direct set because their headers have finite size;
// arrays and structs deliberately do not.
func (r *Runner) bashPPValidateTypeRepresentation(typ syntax.BashPPTypeExpr, active, direct map[string]bool) error {
	switch x := typ.(type) {
	case *syntax.BashPPChanType:
		if r.bashPPGoSource {
			return r.bashPPValidateTypeRepresentation(x.Element, active, make(map[string]bool))
		}
		return fmt.Errorf("BASHPP-ESTRUCT-FIELD-TYPE: unsupported field type %s", bashPPTypeText(typ))
	case *syntax.BashPPTypeParamType:
		return nil
	case *syntax.BashPPUnionType, *syntax.BashPPApproxType:
		return fmt.Errorf("BASHPP-EGENERIC-ARG: %s is a constraint expression, not a concrete type", bashPPTypeText(typ))
	case *syntax.BashPPNamedType:
		if err := bashPPValidateConcreteTypeArgs(x.TypeArgs); err != nil {
			return err
		}
		name := x.Name.Value
		if r.bashPPNativeType(x) {
			_, err := r.bashPPNativeTypeRequest("type", x)
			return err
		}
		if bashPPScalarType(name) {
			return nil
		}
		_, ok := r.bashPPTypes[name]
		if !ok {
			if name == "error" {
				return nil
			}
			return fmt.Errorf("undefined type: %s", name)
		}
		if err := r.bashPPValidateNamedTypeArgs(x); err != nil {
			return err
		}
		// Declaration identity, rather than full instantiation text, makes the
		// traversal terminate even for expanding recursion such as A[*T]. The
		// representation boundary still decides whether that recurrence is legal.
		key := name
		if active[key] {
			if direct[key] {
				return errBashPPTypeCycle
			}
			return nil
		}
		active[key] = true
		defer delete(active, key)
		nextDirect := cloneBashPPTypeSet(direct)
		nextDirect[key] = true
		return r.bashPPValidateTypeRepresentation(r.bashPPInstantiateNamedType(x), active, nextDirect)
	case *syntax.BashPPPointerType:
		if x.Element == nil {
			return fmt.Errorf("BASHPP-EPOINTER-TYPE: invalid pointer type %s", bashPPTypeText(typ))
		}
		return r.bashPPValidateTypeRepresentation(x.Element, active, make(map[string]bool))
	case *syntax.BashPPCollectionType:
		if x.Kind == "array" {
			if x.Length == nil {
				return fmt.Errorf("BASHPP-ECOLLECTION-LENGTH: array length is missing")
			}
			n, err := r.bashPPArrayLength(x.Length.Value)
			if err != nil || n < 0 {
				return fmt.Errorf("BASHPP-ECOLLECTION-LENGTH: invalid array length %s", x.Length.Value)
			}
			return r.bashPPValidateTypeRepresentation(x.Element, active, direct)
		}
		if x.Kind == "map" {
			if !r.bashPPMapKeyType(x.Key) {
				return fmt.Errorf("BASHPP-ECOLLECTION-KEY: unsupported map key type %s", bashPPTypeText(x.Key))
			}
			if err := r.bashPPValidateTypeRepresentation(x.Key, active, make(map[string]bool)); err != nil {
				return err
			}
		}
		return r.bashPPValidateTypeRepresentation(x.Element, active, make(map[string]bool))
	case *syntax.BashPPStructType:
		seenFields := make(map[string]bool)
		for _, field := range x.Fields {
			if field.Embedded {
				if _, ok := bashPPEmbeddedFieldName(field); !ok {
					return fmt.Errorf("BASHPP-ESTRUCT-EMBED: unsupported embedded field type %s", bashPPTypeText(field.FieldTypeExpr))
				}
				_, indirect, definedPointer, iface := r.bashPPEmbeddedTarget(field.FieldTypeExpr)
				if definedPointer {
					name, _ := bashPPEmbeddedFieldName(field)
					return fmt.Errorf("BASHPP-ESTRUCT-EMBED: defined pointer type %s cannot be embedded", name)
				}
				if indirect && iface {
					return fmt.Errorf("BASHPP-ESTRUCT-EMBED: pointer to interface type %s cannot be embedded", bashPPTypeText(field.FieldTypeExpr))
				}
			}
		}
		for _, field := range bashPPFlatFields(x.Fields) {
			if seenFields[field.name] {
				return fmt.Errorf("BASHPP-ESTRUCT-FIELD-DUPLICATE: field %q declared more than once", field.name)
			}
			seenFields[field.name] = true
			if err := r.bashPPValidateTypeRepresentation(field.typ, active, direct); err != nil {
				return err
			}
		}
		return nil
	case *syntax.BashPPFuncType:
		for _, fields := range [][]*syntax.BashPPField{x.Params, x.Results} {
			for _, field := range fields {
				if err := r.bashPPValidateTypeRepresentation(field.FieldTypeExpr, active, make(map[string]bool)); err != nil {
					return err
				}
			}
		}
		return nil
	case *syntax.BashPPInterfaceType:
		if r.bashPPInterfaceHasTypeTerms(x, make(map[*syntax.BashPPInterfaceType]bool)) {
			return fmt.Errorf("BASHPP-EINTERFACE-TYPESET: constraint interface cannot be used as a value type")
		}
		return r.bashPPValidateInterfaceType("anonymous", x)
	}
	return fmt.Errorf("BASHPP-ESTRUCT-FIELD-TYPE: unsupported field type %s", bashPPTypeText(typ))
}

func (r *Runner) bashPPValidateValueType(typ syntax.BashPPTypeExpr, seen map[string]bool) error {
	direct := cloneBashPPTypeSet(seen)
	return r.bashPPValidateTypeRepresentation(typ, seen, direct)
}

func (r *Runner) bashPPStructFields(typ syntax.BashPPTypeExpr) ([]*syntax.BashPPField, string, bool) {
	switch x := typ.(type) {
	case *syntax.BashPPStructType:
		return x.Fields, "struct", true
	case *syntax.BashPPNamedType:
		owner := x.Name.Value
		current := x
		seen := make(map[string]bool)
		for current != nil && !seen[bashPPTypeText(current)] {
			seen[bashPPTypeText(current)] = true
			_, ok := r.bashPPTypes[current.Name.Value]
			if !ok {
				return nil, "", false
			}
			instantiated := r.bashPPInstantiateNamedType(current)
			if st, ok := instantiated.(*syntax.BashPPStructType); ok {
				return st.Fields, owner, true
			}
			current, _ = instantiated.(*syntax.BashPPNamedType)
		}
	}
	return nil, "", false
}

func bashPPFieldType(fields []*syntax.BashPPField, name string) (syntax.BashPPTypeExpr, bool) {
	for _, field := range fields {
		if embedded, ok := bashPPEmbeddedFieldName(field); ok && embedded == name {
			return field.FieldTypeExpr, true
		}
		for _, fieldName := range field.Names {
			if fieldName.Value == name {
				return field.FieldTypeExpr, true
			}
		}
	}
	return nil, false
}

func bashPPFlatFields(fields []*syntax.BashPPField) []struct {
	name string
	typ  syntax.BashPPTypeExpr
} {
	var out []struct {
		name string
		typ  syntax.BashPPTypeExpr
	}
	for _, field := range fields {
		if name, ok := bashPPEmbeddedFieldName(field); ok {
			out = append(out, struct {
				name string
				typ  syntax.BashPPTypeExpr
			}{name, field.FieldTypeExpr})
			continue
		}
		for _, name := range field.Names {
			out = append(out, struct {
				name string
				typ  syntax.BashPPTypeExpr
			}{name.Value, field.FieldTypeExpr})
		}
	}
	return out
}

func (r *Runner) bashPPEvalComposite(lit *syntax.BashPPCompositeLit, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, error) {
	typ := lit.LitType
	if typ == nil {
		typ = expected
	}
	if _, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPCollectionType); ok {
		return r.bashPPEvalCollection(lit, expected)
	}
	fields, typeName, ok := r.bashPPStructFields(typ)
	if !ok {
		return nil, nil, fmt.Errorf("BASHPP-ESTRUCT-TYPE: %s is not a supported struct type", bashPPTypeText(typ))
	}
	if expected != nil && lit.LitType != nil && bashPPTypeText(lit.LitType) != bashPPTypeText(expected) {
		return nil, nil, fmt.Errorf("BASHPP-ESTRUCT-TYPE: cannot use %s as %s", bashPPTypeText(lit.LitType), bashPPTypeText(expected))
	}
	flat := bashPPFlatFields(fields)
	out := make(map[string]any, len(flat))
	meta := &bashPPCollectionMeta{kind: "struct", typ: typ, mapping: make(map[string]*bashPPCollectionMeta, len(flat))}
	for _, field := range flat {
		out[field.name], meta.mapping[field.name] = r.bashPPZeroValue(field.typ)
	}
	keyed, positional := false, false
	for _, elem := range lit.Elems {
		keyed = keyed || elem.Key != nil
		positional = positional || elem.Key == nil
	}
	if keyed && positional {
		return nil, nil, fmt.Errorf("BASHPP-ESTRUCT-MIXED: %s literal cannot mix keyed and positional fields", typeName)
	}
	if positional {
		if len(lit.Elems) != len(flat) {
			return nil, nil, fmt.Errorf("BASHPP-ESTRUCT-POSITIONAL: %s literal requires %d fields; got %d", typeName, len(flat), len(lit.Elems))
		}
		for i, elem := range lit.Elems {
			value, child, err := r.bashPPEvalTypedValue(elem.Value, flat[i].typ)
			if err != nil {
				return nil, nil, err
			}
			out[flat[i].name], meta.mapping[flat[i].name] = value, child
		}
		return out, meta, nil
	}
	seen := make(map[string]bool, len(lit.Elems))
	selections := make([]bashPPSelection, len(lit.Elems))
	for i, elem := range lit.Elems {
		key, ok := elem.Key.(*syntax.BashPPIdent)
		if !ok {
			return nil, nil, fmt.Errorf("%sBASHPP-ESTRUCT-KEY: %s literal field key must be an identifier, not a selector expression", r.bashErrPrefix(elem.Key.Pos()), typeName)
		}
		name := key.Name.Value
		sel := r.bashPPResolveField(typ, name)
		if sel.ambiguous {
			return nil, nil, fmt.Errorf("%sBASHPP-ESTRUCT-AMBIGUOUS: field selector %s.%s is ambiguous", r.bashErrPrefix(key.Pos()), typeName, name)
		}
		if len(sel.edges) == 0 {
			return nil, nil, fmt.Errorf("%sBASHPP-ESTRUCT-UNKNOWN: %s has no field selector %q", r.bashErrPrefix(key.Pos()), typeName, name)
		}
		if seen[name] {
			return nil, nil, fmt.Errorf("%sBASHPP-ESTRUCT-DUPLICATE: field selector %q is supplied more than once", r.bashErrPrefix(key.Pos()), name)
		}
		seen[name] = true
		for _, edge := range sel.edges[:len(sel.edges)-1] {
			if edge.pointer {
				return nil, nil, fmt.Errorf("%sBASHPP-ESTRUCT-KEY-POINTER: field selector %s.%s traverses pointer field %s", r.bashErrPrefix(key.Pos()), typeName, name, edge.name)
			}
		}
		for previous := 0; previous < i; previous++ {
			if bashPPEmbeddedKeyConflict(selections[previous].edges, sel.edges) {
				return nil, nil, fmt.Errorf("%sBASHPP-ESTRUCT-KEY-CONFLICT: field selector %s.%s conflicts with key %s", r.bashErrPrefix(key.Pos()), typeName, name, bashPPEmbedPath(selections[previous].edges))
			}
		}
		selections[i] = sel
	}
	for i, elem := range lit.Elems {
		sel := selections[i]
		value, child, err := r.bashPPEvalTypedValue(elem.Value, sel.fieldType)
		if err != nil {
			return nil, nil, err
		}
		if err := bashPPSetStructSelector(out, meta, sel.edges, value, child); err != nil {
			return nil, nil, fmt.Errorf("%sBASHPP-ESTRUCT-KEY-STORAGE: %v", r.bashErrPrefix(elem.Key.Pos()), err)
		}
	}
	return out, meta, nil
}

func bashPPEmbeddedKeyConflict(left, right []bashPPEmbedEdge) bool {
	shorter, longer := left, right
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	if len(shorter) == len(longer) {
		return false
	}
	for i := range shorter {
		if shorter[i].name != longer[i].name {
			return false
		}
	}
	return true
}

func bashPPEmbedPath(edges []bashPPEmbedEdge) string {
	parts := make([]string, len(edges))
	for i, edge := range edges {
		parts[i] = edge.name
	}
	return strings.Join(parts, ".")
}

func bashPPSetStructSelector(root map[string]any, meta *bashPPCollectionMeta, edges []bashPPEmbedEdge, value any, child *bashPPCollectionMeta) error {
	mapping := root
	currentMeta := meta
	for i, edge := range edges {
		if i == len(edges)-1 {
			mapping[edge.name] = value
			currentMeta.mapping[edge.name] = child
			return nil
		}
		nested, ok := mapping[edge.name].(map[string]any)
		if !ok || currentMeta == nil {
			return fmt.Errorf("promoted path %s no longer names struct storage", bashPPEmbedPath(edges[:i+1]))
		}
		nestedMeta := currentMeta.mapping[edge.name]
		if nestedMeta == nil || nestedMeta.kind != "struct" {
			return fmt.Errorf("promoted path %s no longer names struct storage", bashPPEmbedPath(edges[:i+1]))
		}
		mapping, currentMeta = nested, nestedMeta
	}
	return fmt.Errorf("empty field selector")
}

func (r *Runner) bashPPZeroValue(typ syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta) {
	if r.bashPPNativeType(typ) {
		value, err := r.bashPPNativeTypeRequest("new", typ)
		if err != nil {
			r.exit.fatal(err)
			return nil, nil
		}
		return &value, &bashPPCollectionMeta{kind: "native", typ: typ}
	}
	if _, ok := r.bashPPInterfaceType(typ); ok {
		return "", &bashPPCollectionMeta{kind: "interface", typ: typ, interfaceValue: &bashPPInterfaceValue{nilIface: true}}
	}
	if fields, _, ok := r.bashPPStructFields(typ); ok {
		out := make(map[string]any)
		meta := &bashPPCollectionMeta{kind: "struct", typ: typ, mapping: make(map[string]*bashPPCollectionMeta)}
		for _, field := range bashPPFlatFields(fields) {
			out[field.name], meta.mapping[field.name] = r.bashPPZeroValue(field.typ)
		}
		return out, meta
	}
	if _, ok := r.bashPPPointerType(typ); ok {
		return nil, bashPPPointerMeta(typ)
	}
	return r.bashPPCollectionZero(typ)
}

func (r *Runner) bashPPEvalTypedValue(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, error) {
	if _, ok := r.bashPPInterfaceType(expected); ok {
		iv, vr, err := r.bashPPMakeInterfaceValue(expr, expected)
		if err != nil {
			return nil, nil, err
		}
		value := any(vr.String())
		if vr.Kind == expand.Object {
			value = vr.Obj
		}
		return value, &bashPPCollectionMeta{kind: "interface", typ: expected, interfaceValue: iv}, nil
	}
	if pointerType, ok := r.bashPPPointerType(expected); ok {
		if id, nilIdent := expr.(*syntax.BashPPIdent); nilIdent && id.Name.Value == "nil" {
			return nil, bashPPPointerMeta(expected), nil
		}
		ptr, err := r.bashPPPointerExprValue(expr)
		if err != nil {
			return nil, nil, err
		}
		actual := r.bashPPPointerExprType(expr, ptr)
		if actual == nil {
			actual = &syntax.BashPPPointerType{Element: pointerType.Element}
		}
		if !r.bashPPTypeAssignable(actual, expected) {
			return nil, nil, fmt.Errorf("BASHPP-EASSIGN-MISMATCH: cannot use %s as %s", bashPPTypeText(actual), bashPPTypeText(expected))
		}
		return ptr, bashPPPointerMeta(expected), nil
	}
	if deref, ok := expr.(*syntax.BashPPDerefExpr); ok {
		ptr, err := r.bashPPPointerExprValue(deref.X)
		if err != nil {
			return nil, nil, err
		}
		if ptr == nil {
			return nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
		}
		value, meta, typ, err := ptr.read()
		if err != nil {
			return nil, nil, err
		}
		if expected != nil && bashPPTypeText(typ) != bashPPTypeText(expected) {
			return nil, nil, fmt.Errorf("BASHPP-EASSIGN-MISMATCH: cannot use %s as %s", bashPPTypeText(typ), bashPPTypeText(expected))
		}
		value, meta = bashPPCopyArrayValue(value, meta)
		return value, meta, nil
	}
	if lit, ok := expr.(*syntax.BashPPCompositeLit); ok {
		return r.bashPPEvalComposite(lit, expected)
	}
	if _, ok := expr.(*syntax.BashPPIndexExpr); ok {
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return nil, nil, err
		}
		if err := r.bashPPCheckTypedValue(value, meta, expected); err != nil {
			return nil, nil, err
		}
		value, meta = bashPPCopyArrayValue(value, meta)
		return value, meta, nil
	}
	if _, ok := expr.(*syntax.BashPPSliceExpr); ok {
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return nil, nil, err
		}
		if err := r.bashPPCheckTypedValue(value, meta, expected); err != nil {
			return nil, nil, err
		}
		return value, meta, nil
	}
	if _, ok := expr.(*syntax.BashPPSelectorExpr); ok {
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return nil, nil, err
		}
		if err := r.bashPPCheckTypedValue(value, meta, expected); err != nil {
			return nil, nil, err
		}
		value, meta = bashPPCopyArrayValue(value, meta)
		return value, meta, nil
	}
	if id, ok := expr.(*syntax.BashPPIdent); ok {
		if cell := r.bashPPScope.lookup(id.Name.Value); cell != nil && cell.vr.Kind == expand.Object {
			meta := bashPPCellMeta(cell)
			if err := r.bashPPCheckTypedValue(cell.vr.Obj, meta, expected); err != nil {
				return nil, nil, err
			}
			value, copied := bashPPCopyArrayValue(cell.vr.Obj, meta)
			return value, copied, nil
		}
	}
	value, meta, err := r.bashPPEvalElement(expr, expected)
	if r.bashPPGoSource {
		return value, meta, err
	}
	return value, nil, err
}

func (r *Runner) bashPPCheckTypedValue(value any, meta *bashPPCollectionMeta, expected syntax.BashPPTypeExpr) error {
	if _, native := value.(*bashPPBridgeValue); native && r.bashPPNativeType(expected) {
		return r.goSourceCheckNativeElement(value, expected)
	}
	if iface, ok := r.bashPPInterfaceType(expected); ok {
		if meta == nil || meta.interfaceValue == nil {
			return fmt.Errorf("BASHPP-EASSIGN-MISMATCH: cannot use value as %s", bashPPTypeText(expected))
		}
		if meta.interfaceValue.nilIface {
			return nil
		}
		return r.bashPPImplements(meta.interfaceValue.dynamic, iface)
	}
	if _, ok := r.bashPPPointerType(expected); ok {
		if value == nil {
			return nil
		}
		if _, ok := value.(*bashPPPointer); !ok || meta == nil || !r.bashPPTypeAssignable(meta.typ, expected) {
			return fmt.Errorf("BASHPP-EASSIGN-MISMATCH: cannot use value as %s", bashPPTypeText(expected))
		}
		return nil
	}
	if _, _, ok := r.bashPPStructFields(expected); ok {
		if meta == nil || meta.kind != "struct" || !r.bashPPTypeAssignable(meta.typ, expected) {
			return fmt.Errorf("BASHPP-ESTRUCT-FIELD-TYPE: cannot use value as %s", bashPPTypeText(expected))
		}
		return nil
	}
	if _, ok := expected.(*syntax.BashPPCollectionType); ok {
		if meta == nil || !r.bashPPTypeAssignable(meta.typ, expected) {
			return fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: cannot use value as %s", bashPPTypeText(expected))
		}
		return nil
	}
	if _, ok := r.bashPPUnderlyingType(expected).(*syntax.BashPPCollectionType); ok {
		if meta == nil || !r.bashPPTypeAssignable(meta.typ, expected) {
			return fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: cannot use value as %s", bashPPTypeText(expected))
		}
		return nil
	}
	return r.bashPPCheckCollectionValue(value, expected)
}

func bashPPCellMeta(cell *bashPPCell) *bashPPCollectionMeta {
	if cell == nil {
		return nil
	}
	if cell.pointer {
		return bashPPPointerMeta(cell.declType)
	}
	if cell.valueMeta != nil {
		return cell.valueMeta
	}
	if cell.object != nil {
		return cell.object.collection
	}
	return nil
}

func (r *Runner) bashPPReadExpr(expr syntax.BashPPExpr) (any, *bashPPCollectionMeta, error) {
	if value, meta, handled, err := r.goSourceCollectionCallValue(expr); handled {
		return value, meta, err
	}
	// An index or slice rooted in a dependency-owned value is read through its
	// handle; see bashPPNativeRead in bashpp_native_access.go.
	if value, meta, err, native := r.bashPPNativeRead(expr); native {
		return value, meta, err
	}
	switch x := expr.(type) {
	case *syntax.BashPPCompositeLit:
		return r.bashPPEvalComposite(x, nil)
	case *syntax.BashPPIdent:
		cell := r.bashPPScope.lookup(x.Name.Value)
		if cell != nil && cell.pointer {
			return cell.pointerValue, bashPPPointerMeta(cell.declType), nil
		}
		if cell == nil || cell.vr.Kind != expand.Object {
			return nil, nil, fmt.Errorf("BASHPP-ESELECTOR-ROOT: %s is not a structured value", x.Name.Value)
		}
		return cell.vr.Obj, bashPPCellMeta(cell), nil
	case *syntax.BashPPDerefExpr:
		ptr, err := r.bashPPPointerExprValue(x.X)
		if err != nil {
			return nil, nil, err
		}
		if ptr == nil {
			return nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
		}
		value, meta, _, err := ptr.read()
		return value, meta, err
	case *syntax.BashPPSelectorExpr:
		value, meta, err := r.bashPPReadExpr(x.X)
		if err != nil {
			return nil, nil, err
		}
		if pointer, ok := value.(*bashPPPointer); ok {
			if pointer == nil {
				return nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
			}
			value, meta, _, err = pointer.read()
			if err != nil {
				return nil, nil, err
			}
		}
		if meta == nil || meta.kind != "struct" {
			return nil, nil, fmt.Errorf("BASHPP-ESELECTOR-TYPE: %s has no fields", bashPPExprText(x.X))
		}
		mapping, ok := value.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("BASHPP-ESELECTOR-TYPE: %s has no fields", bashPPExprText(x.X))
		}
		sel := r.bashPPResolveField(meta.typ, x.Sel.Value)
		if sel.ambiguous || len(sel.edges) == 0 {
			return nil, nil, bashPPSelectionError(meta.typ, x.Sel.Value, sel)
		}
		_ = mapping
		return bashPPReadSelection(value, meta, sel.edges)
	case *syntax.BashPPIndexExpr:
		value, meta, err := r.bashPPReadExpr(x.X)
		if err != nil {
			return nil, nil, err
		}
		if pointer, ok := value.(*bashPPPointer); ok {
			if pointer == nil {
				return nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
			}
			value, meta, _, err = pointer.read()
			if err != nil {
				return nil, nil, err
			}
		}
		if meta == nil || meta.kind == "struct" {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-INDEX: value is not a collection")
		}
		collection, ok := r.bashPPUnderlyingType(meta.typ).(*syntax.BashPPCollectionType)
		if !ok {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-INDEX: value is not a collection")
		}
		if meta.kind == "map" {
			key, _, keyErr := r.bashPPEvalElement(x.Index, collection.Key)
			if keyErr != nil {
				return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %v", keyErr)
			}
			canonical := fmt.Sprint(key)
			mapping, valid := value.(map[string]any)
			if !valid && value != nil {
				return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-STORAGE: map payload has type %T", value)
			}
			result, found := mapping[canonical]
			if !found {
				zero, child := r.bashPPZeroValue(collection.Element)
				return zero, child, nil
			}
			return result, meta.mapping[canonical], nil
		}
		i, indexErr := r.bashPPCollectionIndex(x.Index)
		if indexErr != nil {
			return nil, nil, indexErr
		}
		sequence, valid := value.([]any)
		if !valid && value != nil {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-STORAGE: sequence payload has type %T", value)
		}
		if i < 0 || i >= len(sequence) {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for length %d", i, len(sequence))
		}
		if i >= len(meta.sequence) {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-STORAGE: missing element metadata")
		}
		return sequence[i], meta.sequence[i], nil
	case *syntax.BashPPSliceExpr:
		value, meta, err := r.bashPPReadExpr(x.X)
		if err != nil {
			return nil, nil, err
		}
		if pointer, ok := value.(*bashPPPointer); ok {
			if pointer == nil {
				return nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
			}
			value, meta, _, err = pointer.read()
			if err != nil {
				return nil, nil, err
			}
		}
		if meta == nil || meta.kind == "struct" || meta.kind == "map" {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: value is not sliceable")
		}
		collection, ok := r.bashPPUnderlyingType(meta.typ).(*syntax.BashPPCollectionType)
		if !ok {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: value is not sliceable")
		}
		sequence, ok := value.([]any)
		if !ok && value != nil {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: value is not sliceable")
		}
		low, high, max, err := r.bashPPSliceBounds(x, len(sequence), cap(sequence), meta.kind == "slice")
		if err != nil {
			return nil, nil, err
		}
		if max > cap(meta.sequence) {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-STORAGE: missing slice capacity metadata")
		}
		var out []any
		var childSeq []*bashPPCollectionMeta
		if x.SecondColon.IsValid() {
			out = sequence[low:high:max]
			childSeq = meta.sequence[low:high:max]
		} else {
			out = sequence[low:high]
			childSeq = meta.sequence[low:high]
		}
		childType := &syntax.BashPPCollectionType{Kind: "slice", Element: collection.Element}
		child := &bashPPCollectionMeta{kind: "slice", typ: childType, sequence: childSeq}
		if r.bashPPGoSource && sequence == nil {
			return nil, child, nil
		}
		return out, child, nil
	}
	return nil, nil, fmt.Errorf("BASHPP-ESELECTOR-EXPR: unsupported structured expression")
}

func (r *Runner) bashPPStructuredAssign(target, rhs syntax.BashPPExpr) {
	if deref, ok := target.(*syntax.BashPPDerefExpr); ok {
		r.bashPPDerefAssign(deref, rhs)
		return
	}
	root, ok := bashPPCollectionRoot(target)
	cell := r.bashPPScope.lookup(root)
	if ok && cell != nil && cell.pointer {
		ptr, err := r.bashPPAddress(target)
		if err != nil {
			r.errf("%v\n", err)
			r.exit = exitStatus{code: 2}
			return
		}
		if ptr == nil {
			return
		}
		value, meta, err := r.bashPPEvalTypedValue(rhs, ptr.elem)
		if err != nil {
			r.errf("BASHPP-EASSIGN-MISMATCH: %v\n", err)
			r.exit = exitStatus{code: 2}
			return
		}
		if ptr.target.object != nil && ptr.target.object.readonly {
			r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through pointer\n", ptr.target.object.owner)
			r.exit = exitStatus{code: 2}
			return
		}
		if ptr.target.constant || ptr.target.vr.ReadOnly {
			r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value through pointer\n")
			r.exit = exitStatus{code: 2}
			return
		}
		parent, parentMeta, _, err := ptr.readParent()
		if err != nil {
			r.errf("%v\n", err)
			r.exit = exitStatus{code: 2}
			return
		}
		if parentMeta == nil {
			r.errf("BASHPP-ESELECTOR-TYPE: assignment parent is not a structured value\n")
			r.exit = exitStatus{code: 2}
			return
		}
		last := ptr.path[len(ptr.path)-1]
		if last.field != "" {
			parent.(map[string]any)[last.field] = value
			parentMeta.mapping[last.field] = meta
		} else {
			parent.([]any)[last.index] = value
			parentMeta.sequence[last.index] = meta
		}
		return
	}
	if !ok || cell == nil || cell.object == nil {
		r.errf("BASHPP-ESELECTOR-ASSIGN: target is not a structured value\n")
		r.exit = exitStatus{code: 2}
		return
	}
	path := strings.TrimPrefix(bashPPExprText(target), root)
	if cell.object.readonly {
		if root != cell.object.owner {
			r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through alias %q and path %s\n", cell.object.owner, root, path)
		} else {
			kind := "field"
			parentExpr := bashPPParentExpr(target)
			_, parentMeta, _ := r.bashPPReadExpr(parentExpr)
			if parentMeta != nil && parentMeta.kind != "struct" {
				kind = "slice"
				if parentMeta.kind == "map" {
					kind = "map"
				}
			}
			if kind == "field" {
				detail := path
				if dot := strings.LastIndex(path, "."); dot >= 0 {
					detail = path[dot:]
				}
				r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through field %s\n", cell.object.owner, detail)
			} else {
				r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through %s path %s\n", cell.object.owner, kind, path)
			}
		}
		r.exit = exitStatus{code: 2}
		return
	}
	if bashPPCellMeta(cell) == nil {
		r.errf("BASHPP-ESELECTOR-ASSIGN: target is not a supported typed structured value\n")
		r.exit = exitStatus{code: 2}
		return
	}
	parentExpr := bashPPParentExpr(target)
	parent, parentMeta, err := r.bashPPReadExpr(parentExpr)
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return
	}
	if parentMeta == nil {
		r.errf("BASHPP-ESELECTOR-TYPE: assignment parent is not a structured value\n")
		r.exit = exitStatus{code: 2}
		return
	}
	var expected syntax.BashPPTypeExpr
	switch x := target.(type) {
	case *syntax.BashPPSelectorExpr:
		sel := r.bashPPResolveField(parentMeta.typ, x.Sel.Value)
		if sel.ambiguous || len(sel.edges) == 0 {
			err = bashPPSelectionError(parentMeta.typ, x.Sel.Value, sel)
			break
		}
		expected = sel.fieldType
		value, child, valueErr := r.bashPPEvalTypedValue(rhs, expected)
		if valueErr != nil {
			err = valueErr
			break
		}
		if len(sel.edges) > 1 {
			parent, parentMeta, err = bashPPReadSelection(parent, parentMeta, sel.edges[:len(sel.edges)-1])
			if err != nil {
				break
			}
			parent, parentMeta, err = bashPPDerefEmbedded(parent, parentMeta)
			if err != nil {
				break
			}
		}
		last := sel.edges[len(sel.edges)-1].name
		mapping, ok := parent.(map[string]any)
		if !ok || parentMeta == nil || parentMeta.kind != "struct" {
			err = fmt.Errorf("BASHPP-ESELECTOR-TYPE: assignment parent is not struct storage")
			break
		}
		mapping[last] = value
		parentMeta.mapping[last] = child
	case *syntax.BashPPIndexExpr:
		collection, found := r.bashPPUnderlyingType(parentMeta.typ).(*syntax.BashPPCollectionType)
		if !found {
			err = fmt.Errorf("BASHPP-ECOLLECTION-ASSIGN: indexed target is not a collection")
			break
		}
		expected = collection.Element
		var savedKey any
		var savedIndex int
		if r.bashPPGoSource {
			if parentMeta.kind == "map" {
				savedKey, _, err = r.bashPPEvalElement(x.Index, collection.Key)
			} else {
				savedIndex, err = r.bashPPCollectionIndex(x.Index)
			}
			if err != nil {
				break
			}
		}
		value, child, valueErr := r.bashPPEvalTypedValue(rhs, expected)
		if valueErr != nil {
			err = valueErr
			break
		}
		if parentMeta.kind == "map" {
			if parent == nil {
				err = fmt.Errorf("BASHPP-ENIL-MAP: assignment to nil map")
				break
			}
			key, keyErr := savedKey, error(nil)
			if !r.bashPPGoSource {
				key, _, keyErr = r.bashPPEvalElement(x.Index, collection.Key)
			}
			if keyErr != nil {
				err = keyErr
				break
			}
			canonical := fmt.Sprint(key)
			mapping, valid := parent.(map[string]any)
			if !valid || mapping == nil {
				err = fmt.Errorf("BASHPP-ENIL-MAP: assignment to nil map")
				break
			}
			mapping[canonical] = value
			parentMeta.mapping[canonical] = child
		} else {
			i, indexErr := savedIndex, error(nil)
			if !r.bashPPGoSource {
				i, indexErr = r.bashPPCollectionIndex(x.Index)
			}
			if indexErr != nil {
				err = indexErr
				break
			}
			sequence, valid := parent.([]any)
			if !valid && parent != nil {
				err = fmt.Errorf("BASHPP-ECOLLECTION-STORAGE: invalid sequence payload")
				break
			}
			if i < 0 || i >= len(sequence) {
				err = fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for length %d", i, len(sequence))
				break
			}
			if i >= len(parentMeta.sequence) {
				err = fmt.Errorf("BASHPP-ECOLLECTION-STORAGE: missing element metadata")
				break
			}
			sequence[i], parentMeta.sequence[i] = value, child
		}
	}
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
	}
}

func bashPPParentExpr(expr syntax.BashPPExpr) syntax.BashPPExpr {
	switch x := expr.(type) {
	case *syntax.BashPPSelectorExpr:
		return x.X
	case *syntax.BashPPIndexExpr:
		return x.X
	}
	return expr
}

// bashPPCompositeAddress materializes `&T{…}` — a composite literal taken by
// address. Go allows it although the literal is not a variable: the result is
// a pointer to a fresh value with its own lifetime, so the literal is stored
// in an anonymous cell which the returned pointer owns.
func (r *Runner) bashPPCompositeAddress(lit *syntax.BashPPCompositeLit) (*bashPPPointer, error) {
	typ := lit.LitType
	if typ == nil {
		return nil, fmt.Errorf("BASHPP-ENONADDRESSABLE: composite literal has no type")
	}
	value, meta, err := r.bashPPEvalComposite(lit, nil)
	if err != nil {
		return nil, err
	}
	cell := &bashPPCell{declType: typ}
	if named, ok := typ.(*syntax.BashPPNamedType); ok && named.Name != nil {
		cell.typeName = named.Name.Value
	}
	bashPPStoreCellValue(cell, value, meta)
	return &bashPPPointer{target: cell, elem: typ}, nil
}

// bashPPRepresentableScalar converts an untyped constant toward the type it is
// being stored in, the way Go does. `p.X = 1e9` assigns an int field because
// 1e9 denotes an exact integer, even though the constant is written in
// floating form; a constant that is not exactly an integer is left alone so
// the assignment is still refused.
func (r *Runner) bashPPRepresentableScalar(scalar bashPPScalar, expected syntax.BashPPTypeExpr) bashPPScalar {
	if scalar.typ != "" || scalar.value == nil || scalar.value.Kind() != constant.Float || expected == nil {
		return scalar
	}
	name, ok := r.bashPPUnderlyingType(expected).(*syntax.BashPPNamedType)
	if !ok || !bashPPIntegerType(name.Name.Value) {
		return scalar
	}
	if integer := constant.ToInt(scalar.value); integer.Kind() == constant.Int {
		scalar.value = integer
	}
	return scalar
}
