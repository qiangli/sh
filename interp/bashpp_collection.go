// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"go/constant"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPCollectionMeta keeps the Go collection identity which a JSON-shaped
// object payload cannot express. Nested metadata follows the payload exactly.
type bashPPCollectionMeta struct {
	kind     string
	typ      syntax.BashPPTypeExpr
	sequence []*bashPPCollectionMeta
	mapping  map[string]*bashPPCollectionMeta
}

func bashPPArrayMeta(meta *bashPPCollectionMeta) bool {
	return meta != nil && (meta.kind == "array" || meta.kind == "inferred-array")
}

// bashPPCopyArrayValue applies Go's value semantics to arrays without
// accidentally deep-copying slice or map elements, which remain reference
// values. Nested array elements are copied recursively.
func bashPPCopyArrayValue(value any, meta *bashPPCollectionMeta) (any, *bashPPCollectionMeta) {
	if !bashPPArrayMeta(meta) {
		return value, meta
	}
	sequence, ok := value.([]any)
	if !ok {
		return value, meta
	}
	out := append([]any(nil), sequence...)
	metaCopy := *meta
	metaCopy.sequence = append([]*bashPPCollectionMeta(nil), meta.sequence...)
	for i, child := range metaCopy.sequence {
		if bashPPArrayMeta(child) {
			out[i], metaCopy.sequence[i] = bashPPCopyArrayValue(out[i], child)
		}
	}
	return out, &metaCopy
}

func bashPPCloneCollectionMeta(meta *bashPPCollectionMeta, seen map[*bashPPCollectionMeta]*bashPPCollectionMeta) *bashPPCollectionMeta {
	if meta == nil {
		return nil
	}
	if done := seen[meta]; done != nil {
		return done
	}
	out := *meta
	seen[meta] = &out
	out.sequence = make([]*bashPPCollectionMeta, len(meta.sequence))
	for i, child := range meta.sequence {
		out.sequence[i] = bashPPCloneCollectionMeta(child, seen)
	}
	if meta.mapping != nil {
		out.mapping = make(map[string]*bashPPCollectionMeta, len(meta.mapping))
		for key, child := range meta.mapping {
			out.mapping[key] = bashPPCloneCollectionMeta(child, seen)
		}
	}
	return &out
}

func bashPPTypeText(typ syntax.BashPPTypeExpr) string {
	switch x := typ.(type) {
	case *syntax.BashPPNamedType:
		return x.Name.Value
	case *syntax.BashPPCollectionType:
		if x.Kind == "map" {
			return "map[" + bashPPTypeText(x.Key) + "]" + bashPPTypeText(x.Element)
		}
		length := ""
		if x.Length != nil {
			length = x.Length.Value
		}
		return "[" + length + "]" + bashPPTypeText(x.Element)
	}
	return "<inferred>"
}

func (r *Runner) bashPPEvalCollection(lit *syntax.BashPPCompositeLit, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, error) {
	typ := lit.LitType
	if typ == nil {
		typ = expected
	}
	collection, ok := typ.(*syntax.BashPPCollectionType)
	if !ok {
		return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-TYPE: collection literal requires array, slice, or map type; got %s", bashPPTypeText(typ))
	}
	if err := r.bashPPValidateCollectionType(collection); err != nil {
		return nil, nil, err
	}
	if expected != nil && lit.LitType != nil && bashPPTypeText(typ) != bashPPTypeText(expected) {
		return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: cannot use %s as %s element", bashPPTypeText(typ), bashPPTypeText(expected))
	}
	meta := &bashPPCollectionMeta{kind: collection.Kind, typ: typ}
	if collection.Kind == "map" {
		if !r.bashPPMapKeyType(collection.Key) {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-KEY: unsupported map key type %s", bashPPTypeText(collection.Key))
		}
		out := make(map[string]any, len(lit.Elems))
		meta.mapping = make(map[string]*bashPPCollectionMeta, len(lit.Elems))
		for _, elem := range lit.Elems {
			if elem.Key == nil {
				return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-MAP-ELEMENT: map literal element requires key:value")
			}
			key, _, err := r.bashPPEvalElement(elem.Key, collection.Key)
			if err != nil {
				return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %v", err)
			}
			canonical := fmt.Sprint(key)
			if _, exists := out[canonical]; exists {
				return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-DUPLICATE: duplicate map key %q", canonical)
			}
			value, child, err := r.bashPPEvalElement(elem.Value, collection.Element)
			if err != nil {
				return nil, nil, err
			}
			out[canonical], meta.mapping[canonical] = value, child
		}
		return out, meta, nil
	}

	fixed := -1
	if collection.Kind == "array" {
		var err error
		fixed, err = strconv.Atoi(collection.Length.Value)
		if err != nil || fixed < 0 {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-LENGTH: invalid array length %s", collection.Length.Value)
		}
	}
	values := make(map[int]any, len(lit.Elems))
	children := make(map[int]*bashPPCollectionMeta, len(lit.Elems))
	next, high := 0, -1
	for _, elem := range lit.Elems {
		index := next
		if elem.Key != nil {
			key, err := r.bashPPCollectionIndex(elem.Key)
			if err != nil {
				return nil, nil, err
			}
			index = key
		}
		if index < 0 || fixed >= 0 && index >= fixed {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for %s", index, bashPPTypeText(typ))
		}
		if _, exists := values[index]; exists {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-DUPLICATE: duplicate index %d", index)
		}
		value, child, err := r.bashPPEvalElement(elem.Value, collection.Element)
		if err != nil {
			return nil, nil, err
		}
		values[index], children[index] = value, child
		if index > high {
			high = index
		}
		next = index + 1
	}
	length := high + 1
	if fixed >= 0 {
		length = fixed
	}
	out := make([]any, length)
	meta.sequence = make([]*bashPPCollectionMeta, length)
	for i := range out {
		out[i], meta.sequence[i] = r.bashPPCollectionZero(collection.Element)
	}
	for i, value := range values {
		out[i], meta.sequence[i] = value, children[i]
	}
	return out, meta, nil
}

func (r *Runner) bashPPValidateCollectionType(typ syntax.BashPPTypeExpr) error {
	switch x := typ.(type) {
	case *syntax.BashPPNamedType:
		name := x.Name.Value
		if bashPPScalarType(name) {
			return nil
		}
		decl, ok := r.bashPPTypes[name]
		if !ok {
			return fmt.Errorf("BASHPP-ECOLLECTION-TYPE: undefined element type %s", name)
		}
		if !bashPPScalarType(strings.TrimPrefix(decl.underlying, "*")) {
			return fmt.Errorf("BASHPP-ECOLLECTION-TYPE: unsupported element type %s", name)
		}
		return nil
	case *syntax.BashPPCollectionType:
		if x.Kind == "array" {
			if x.Length == nil {
				return fmt.Errorf("BASHPP-ECOLLECTION-LENGTH: array length is missing")
			}
			if _, err := strconv.Atoi(x.Length.Value); err != nil {
				return fmt.Errorf("BASHPP-ECOLLECTION-LENGTH: invalid array length %s", x.Length.Value)
			}
		}
		if x.Kind == "map" {
			if !r.bashPPMapKeyType(x.Key) {
				return fmt.Errorf("BASHPP-ECOLLECTION-KEY: unsupported map key type %s", bashPPTypeText(x.Key))
			}
			if err := r.bashPPValidateCollectionType(x.Key); err != nil {
				return err
			}
		}
		return r.bashPPValidateCollectionType(x.Element)
	}
	return fmt.Errorf("BASHPP-ECOLLECTION-TYPE: unsupported collection type %s", bashPPTypeText(typ))
}

func (r *Runner) bashPPCollectionZero(typ syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta) {
	switch x := typ.(type) {
	case *syntax.BashPPNamedType:
		name := x.Name.Value
		if decl, ok := r.bashPPTypes[name]; ok {
			name = strings.TrimPrefix(decl.underlying, "*")
		}
		switch {
		case name == "string":
			return "", nil
		case name == "bool":
			return false, nil
		case name == "float32" || name == "float64":
			return float64(0), nil
		default:
			return 0, nil
		}
	case *syntax.BashPPCollectionType:
		meta := &bashPPCollectionMeta{kind: x.Kind, typ: x}
		if x.Kind == "map" {
			meta.mapping = make(map[string]*bashPPCollectionMeta)
			return map[string]any{}, meta
		}
		length := 0
		if x.Kind == "array" {
			length, _ = strconv.Atoi(x.Length.Value)
		}
		values := make([]any, length)
		meta.sequence = make([]*bashPPCollectionMeta, length)
		for i := range values {
			values[i], meta.sequence[i] = r.bashPPCollectionZero(x.Element)
		}
		return values, meta
	}
	return nil, nil
}

func (r *Runner) bashPPEvalElement(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, error) {
	if lit, ok := expr.(*syntax.BashPPCompositeLit); ok {
		return r.bashPPEvalCollection(lit, expected)
	}
	if index, ok := expr.(*syntax.BashPPIndexExpr); ok {
		value, meta, err := r.bashPPCollectionRead(index)
		if err != nil {
			return nil, nil, err
		}
		if err := r.bashPPCheckCollectionValue(value, expected); err != nil {
			return nil, nil, err
		}
		value, meta = bashPPCopyArrayValue(value, meta)
		return value, meta, nil
	}
	scalar, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: %v", err)
	}
	value := bashPPScalarAny(scalar.value)
	if err := r.bashPPCheckCollectionValue(value, expected); err != nil {
		return nil, nil, err
	}
	return value, nil, nil
}

func bashPPScalarAny(value constant.Value) any {
	switch value.Kind() {
	case constant.String:
		return constant.StringVal(value)
	case constant.Bool:
		return constant.BoolVal(value)
	case constant.Int:
		if n, ok := constant.Int64Val(value); ok {
			return int(n)
		}
	case constant.Float:
		if n, ok := constant.Float64Val(value); ok {
			return n
		}
	}
	return bashPPScalarString(value)
}

func (r *Runner) bashPPCheckCollectionValue(value any, expected syntax.BashPPTypeExpr) error {
	name, ok := expected.(*syntax.BashPPNamedType)
	if !ok {
		return fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: scalar cannot be used as %s", bashPPTypeText(expected))
	}
	typ := name.Name.Value
	for {
		decl, found := r.bashPPTypes[typ]
		if !found {
			break
		}
		typ = strings.TrimPrefix(decl.underlying, "*")
	}
	valid := false
	switch {
	case typ == "string":
		_, valid = value.(string)
	case typ == "bool":
		_, valid = value.(bool)
	case bashPPIntegerType(typ):
		_, valid = value.(int)
	case typ == "float32" || typ == "float64":
		switch value.(type) {
		case int, float64:
			valid = true
		}
	}
	if !valid {
		return fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: cannot use %T value as %s", value, name.Name.Value)
	}
	return nil
}

func (r *Runner) bashPPMapKeyType(typ syntax.BashPPTypeExpr) bool {
	name, ok := typ.(*syntax.BashPPNamedType)
	if !ok {
		return false
	}
	t := name.Name.Value
	return t == "string" || t == "bool" || bashPPIntegerType(t)
}

func (r *Runner) bashPPCollectionIndex(expr syntax.BashPPExpr) (int, error) {
	v, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		return 0, fmt.Errorf("BASHPP-ECOLLECTION-INDEX: %v", err)
	}
	n, ok := constant.Int64Val(v.value)
	if !ok || int64(int(n)) != n {
		return 0, fmt.Errorf("BASHPP-ECOLLECTION-INDEX: index must be an integer")
	}
	return int(n), nil
}

func (r *Runner) bashPPCollectionRead(index *syntax.BashPPIndexExpr) (any, *bashPPCollectionMeta, error) {
	var value any
	var meta *bashPPCollectionMeta
	switch x := index.X.(type) {
	case *syntax.BashPPIdent:
		cell := r.bashPPScope.lookup(x.Name.Value)
		if cell == nil || cell.vr.Kind != expand.Object || cell.object == nil || cell.object.collection == nil {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-INDEX: %s is not a collection", x.Name.Value)
		}
		value, meta = cell.vr.Obj, cell.object.collection
	case *syntax.BashPPIndexExpr:
		var err error
		value, meta, err = r.bashPPCollectionRead(x)
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-INDEX: unsupported indexed value")
	}
	if meta == nil {
		return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-INDEX: value is not a collection")
	}
	if meta.kind == "map" {
		collection := meta.typ.(*syntax.BashPPCollectionType)
		key, _, err := r.bashPPEvalElement(index.Index, collection.Key)
		if err != nil {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-KEY: %v", err)
		}
		canonical := fmt.Sprint(key)
		mapping := value.(map[string]any)
		result, found := mapping[canonical]
		if !found {
			zero, child := r.bashPPCollectionZero(collection.Element)
			return zero, child, nil
		}
		return result, meta.mapping[canonical], nil
	}
	i, err := r.bashPPCollectionIndex(index.Index)
	if err != nil {
		return nil, nil, err
	}
	sequence := value.([]any)
	if i < 0 || i >= len(sequence) {
		return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for length %d", i, len(sequence))
	}
	return sequence[i], meta.sequence[i], nil
}

func bashPPCollectionRoot(expr syntax.BashPPExpr) (string, bool) {
	switch x := expr.(type) {
	case *syntax.BashPPIdent:
		return x.Name.Value, true
	case *syntax.BashPPIndexExpr:
		return bashPPCollectionRoot(x.X)
	}
	return "", false
}

func bashPPExprText(expr syntax.BashPPExpr) string {
	switch x := expr.(type) {
	case *syntax.BashPPIdent:
		return x.Name.Value
	case *syntax.BashPPBasicLit:
		return x.Value.Value
	case *syntax.BashPPIndexExpr:
		return bashPPExprText(x.X) + "[" + bashPPExprText(x.Index) + "]"
	}
	return "?"
}

func (r *Runner) bashPPCollectionAssign(target *syntax.BashPPIndexExpr, rhs syntax.BashPPExpr) {
	root, ok := bashPPCollectionRoot(target)
	cell := r.bashPPScope.lookup(root)
	if !ok || cell == nil || cell.object == nil || cell.object.collection == nil {
		r.errf("BASHPP-ECOLLECTION-ASSIGN: indexed target is not a collection\n")
		r.exit = exitStatus{code: 2}
		return
	}
	path := strings.TrimPrefix(bashPPExprText(target), root)
	if cell.object.readonly {
		if root != cell.object.owner {
			r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through alias %q and path %s\n", cell.object.owner, root, path)
		} else {
			parentMeta := cell.object.collection
			if nested, ok := target.X.(*syntax.BashPPIndexExpr); ok {
				_, parentMeta, _ = r.bashPPCollectionRead(nested)
			}
			kind := "slice"
			if parentMeta != nil && parentMeta.kind == "map" {
				kind = "map"
			}
			r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through %s path %s\n", cell.object.owner, kind, path)
		}
		r.exit = exitStatus{code: 2}
		return
	}

	var parent any
	var meta *bashPPCollectionMeta
	if id, ok := target.X.(*syntax.BashPPIdent); ok {
		_ = id
		parent, meta = cell.vr.Obj, cell.object.collection
	} else {
		var err error
		parent, meta, err = r.bashPPCollectionRead(target.X.(*syntax.BashPPIndexExpr))
		if err != nil {
			r.errf("%v\n", err)
			r.exit = exitStatus{code: 2}
			return
		}
	}
	if meta == nil {
		r.errf("BASHPP-ECOLLECTION-ASSIGN: indexed target is not a collection\n")
		r.exit = exitStatus{code: 2}
		return
	}
	typ := meta.typ.(*syntax.BashPPCollectionType)
	value, child, err := r.bashPPEvalElement(rhs, typ.Element)
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return
	}
	if meta.kind == "map" {
		key, _, keyErr := r.bashPPEvalElement(target.Index, typ.Key)
		if keyErr != nil {
			r.errf("BASHPP-ECOLLECTION-KEY: %v\n", keyErr)
			r.exit = exitStatus{code: 2}
			return
		}
		canonical := fmt.Sprint(key)
		parent.(map[string]any)[canonical] = value
		meta.mapping[canonical] = child
		return
	}
	i, indexErr := r.bashPPCollectionIndex(target.Index)
	if indexErr != nil {
		r.errf("%v\n", indexErr)
		r.exit = exitStatus{code: 2}
		return
	}
	sequence := parent.([]any)
	if i < 0 || i >= len(sequence) {
		r.errf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for length %d\n", i, len(sequence))
		r.exit = exitStatus{code: 2}
		return
	}
	sequence[i], meta.sequence[i] = value, child
}
