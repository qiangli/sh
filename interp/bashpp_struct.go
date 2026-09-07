// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"errors"
	"fmt"
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
	case *syntax.BashPPTypeParamType:
		return nil
	case *syntax.BashPPUnionType, *syntax.BashPPApproxType:
		return fmt.Errorf("BASHPP-EGENERIC-ARG: %s is a constraint expression, not a concrete type", bashPPTypeText(typ))
	case *syntax.BashPPNamedType:
		if err := bashPPValidateConcreteTypeArgs(x.TypeArgs); err != nil {
			return err
		}
		name := x.Name.Value
		if bashPPScalarType(name) {
			return nil
		}
		_, ok := r.bashPPTypes[name]
		if !ok {
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
		name := x.Name.Value
		seen := make(map[string]bool)
		for !seen[name] {
			seen[name] = true
			decl, ok := r.bashPPTypes[name]
			if !ok {
				return nil, "", false
			}
			if len(x.TypeArgs) > 0 {
				if st, ok := r.bashPPInstantiateNamedType(x).(*syntax.BashPPStructType); ok {
					return st.Fields, x.Name.Value, true
				}
			}
			if decl.underlying == "struct" {
				return decl.fields, x.Name.Value, true
			}
			name = strings.TrimPrefix(decl.underlying, "*")
		}
	}
	return nil, "", false
}

func bashPPFieldType(fields []*syntax.BashPPField, name string) (syntax.BashPPTypeExpr, bool) {
	for _, field := range fields {
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
	for _, elem := range lit.Elems {
		key, ok := elem.Key.(*syntax.BashPPIdent)
		if !ok {
			return nil, nil, fmt.Errorf("BASHPP-ESTRUCT-KEY: %s literal field name must be an identifier", typeName)
		}
		name := key.Name.Value
		fieldType, found := bashPPFieldType(fields, name)
		if !found {
			return nil, nil, fmt.Errorf("BASHPP-ESTRUCT-UNKNOWN: %s has no field %q", typeName, name)
		}
		if seen[name] {
			return nil, nil, fmt.Errorf("BASHPP-ESTRUCT-DUPLICATE: field %q is supplied more than once", name)
		}
		seen[name] = true
		value, child, err := r.bashPPEvalTypedValue(elem.Value, fieldType)
		if err != nil {
			return nil, nil, err
		}
		out[name], meta.mapping[name] = value, child
	}
	return out, meta, nil
}

func (r *Runner) bashPPZeroValue(typ syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta) {
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
	value, _, err := r.bashPPEvalElement(expr, expected)
	return value, nil, err
}

func (r *Runner) bashPPCheckTypedValue(value any, meta *bashPPCollectionMeta, expected syntax.BashPPTypeExpr) error {
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
		result, found := mapping[x.Sel.Value]
		if !found {
			return nil, nil, fmt.Errorf("BASHPP-ESELECTOR-UNKNOWN: %s has no field %q", bashPPTypeText(meta.typ), x.Sel.Value)
		}
		return result, meta.mapping[x.Sel.Value], nil
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
		collection := meta.typ.(*syntax.BashPPCollectionType)
		if meta.kind == "map" {
			key, _, keyErr := r.bashPPEvalElement(x.Index, collection.Key)
			if keyErr != nil {
				return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %v", keyErr)
			}
			canonical := fmt.Sprint(key)
			result, found := value.(map[string]any)[canonical]
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
		sequence := value.([]any)
		if i < 0 || i >= len(sequence) {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for length %d", i, len(sequence))
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
		collection := meta.typ.(*syntax.BashPPCollectionType)
		sequence, ok := value.([]any)
		if !ok && value != nil {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: value is not sliceable")
		}
		low, high, max, err := r.bashPPSliceBounds(x, len(sequence), cap(sequence), meta.kind == "slice")
		if err != nil {
			return nil, nil, err
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
		fields, _, found := r.bashPPStructFields(parentMeta.typ)
		if !found {
			err = fmt.Errorf("BASHPP-ESELECTOR-TYPE: target has no fields")
			break
		}
		expected, found = bashPPFieldType(fields, x.Sel.Value)
		if !found {
			err = fmt.Errorf("BASHPP-ESELECTOR-UNKNOWN: %s has no field %q", bashPPTypeText(parentMeta.typ), x.Sel.Value)
			break
		}
		value, child, valueErr := r.bashPPEvalTypedValue(rhs, expected)
		if valueErr != nil {
			err = valueErr
			break
		}
		parent.(map[string]any)[x.Sel.Value] = value
		parentMeta.mapping[x.Sel.Value] = child
	case *syntax.BashPPIndexExpr:
		collection, found := r.bashPPUnderlyingType(parentMeta.typ).(*syntax.BashPPCollectionType)
		if !found {
			err = fmt.Errorf("BASHPP-ECOLLECTION-ASSIGN: indexed target is not a collection")
			break
		}
		expected = collection.Element
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
			key, _, keyErr := r.bashPPEvalElement(x.Index, collection.Key)
			if keyErr != nil {
				err = keyErr
				break
			}
			canonical := fmt.Sprint(key)
			parent.(map[string]any)[canonical] = value
			parentMeta.mapping[canonical] = child
		} else {
			i, indexErr := r.bashPPCollectionIndex(x.Index)
			if indexErr != nil {
				err = indexErr
				break
			}
			sequence := parent.([]any)
			if i < 0 || i >= len(sequence) {
				err = fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for length %d", i, len(sequence))
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
