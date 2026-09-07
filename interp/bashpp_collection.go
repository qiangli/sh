// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	goast "go/ast"
	"go/constant"
	goparser "go/parser"
	gotoken "go/token"
	"strconv"
	"strings"

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

func bashPPValueMeta(meta *bashPPCollectionMeta) bool {
	return bashPPArrayMeta(meta) || meta != nil && meta.kind == "struct"
}

// bashPPCopyArrayValue applies Go's value semantics to arrays without
// accidentally deep-copying slice or map elements, which remain reference
// values. Nested array elements are copied recursively.
func bashPPCopyArrayValue(value any, meta *bashPPCollectionMeta) (any, *bashPPCollectionMeta) {
	if !bashPPValueMeta(meta) {
		return value, meta
	}
	metaCopy := *meta
	if meta.kind == "struct" {
		mapping, ok := value.(map[string]any)
		if !ok {
			return value, meta
		}
		out := make(map[string]any, len(mapping))
		metaCopy.mapping = make(map[string]*bashPPCollectionMeta, len(meta.mapping))
		for field, item := range mapping {
			child := meta.mapping[field]
			if bashPPValueMeta(child) {
				item, child = bashPPCopyArrayValue(item, child)
			}
			out[field], metaCopy.mapping[field] = item, child
		}
		return out, &metaCopy
	}
	sequence, ok := value.([]any)
	if !ok {
		return value, meta
	}
	out := append([]any(nil), sequence...)
	metaCopy.sequence = append([]*bashPPCollectionMeta(nil), meta.sequence...)
	for i, child := range metaCopy.sequence {
		if bashPPValueMeta(child) {
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
		if len(x.TypeArgs) == 0 {
			return x.Name.Value
		}
		var args []string
		for _, arg := range x.TypeArgs {
			args = append(args, bashPPTypeText(arg.ArgType))
		}
		return x.Name.Value + "[" + strings.Join(args, ", ") + "]"
	case *syntax.BashPPTypeParamType:
		return x.Name.Value
	case *syntax.BashPPUnionType:
		var terms []string
		for _, term := range x.Terms {
			terms = append(terms, bashPPTypeText(term))
		}
		return strings.Join(terms, " | ")
	case *syntax.BashPPApproxType:
		return "~" + bashPPTypeText(x.Term)
	case *syntax.BashPPCollectionType:
		if x.Kind == "map" {
			return "map[" + bashPPTypeText(x.Key) + "]" + bashPPTypeText(x.Element)
		}
		length := ""
		if x.Length != nil {
			length = x.Length.Value
		}
		return "[" + length + "]" + bashPPTypeText(x.Element)
	case *syntax.BashPPStructType:
		return "struct"
	case *syntax.BashPPPointerType:
		return "*" + bashPPTypeText(x.Element)
	case *syntax.BashPPInterfaceType:
		return "interface"
	}
	return "<inferred>"
}

func (r *Runner) bashPPUnderlyingType(typ syntax.BashPPTypeExpr) syntax.BashPPTypeExpr {
	seen := make(map[string]bool)
	for {
		name, ok := typ.(*syntax.BashPPNamedType)
		if !ok {
			return typ
		}
		decl, found := r.bashPPTypes[name.Name.Value]
		if !found || decl.typeExpr == nil || seen[bashPPTypeText(name)] {
			return typ
		}
		seen[bashPPTypeText(name)] = true
		typ = r.bashPPInstantiateNamedType(name)
	}
}

func (r *Runner) bashPPTypeAssignable(actual, expected syntax.BashPPTypeExpr) bool {
	if expected == nil {
		return true
	}
	actual = r.bashPPCanonicalAssignableType(actual)
	expected = r.bashPPCanonicalAssignableType(expected)
	if bashPPTypeText(actual) == bashPPTypeText(expected) {
		return true
	}
	_, actualNamed := actual.(*syntax.BashPPNamedType)
	_, expectedNamed := expected.(*syntax.BashPPNamedType)
	if actualNamed && expectedNamed {
		return false
	}
	return bashPPTypeText(r.bashPPUnderlyingType(actual)) == bashPPTypeText(r.bashPPUnderlyingType(expected))
}

func (r *Runner) bashPPCanonicalAssignableType(typ syntax.BashPPTypeExpr) syntax.BashPPTypeExpr {
	seen := make(map[string]bool)
	for {
		name, ok := typ.(*syntax.BashPPNamedType)
		if !ok || seen[name.Name.Value] {
			return typ
		}
		decl, found := r.bashPPTypes[name.Name.Value]
		if !found || !decl.alias || decl.typeExpr == nil {
			return typ
		}
		seen[name.Name.Value] = true
		typ = r.bashPPInstantiateNamedType(name)
	}
}

func (r *Runner) bashPPEvalCollection(lit *syntax.BashPPCompositeLit, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, error) {
	typ := lit.LitType
	if typ == nil {
		typ = expected
	}
	shape := r.bashPPUnderlyingType(typ)
	collection, ok := shape.(*syntax.BashPPCollectionType)
	if !ok {
		return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-TYPE: collection literal requires array, slice, or map type; got %s", bashPPTypeText(typ))
	}
	if err := r.bashPPValidateCollectionType(collection); err != nil {
		return nil, nil, err
	}
	inferredToArray := false
	if expected != nil && lit.LitType != nil {
		if actualCollection := collection; actualCollection.Kind == "inferred-array" {
			if expectedCollection, ok := r.bashPPUnderlyingType(expected).(*syntax.BashPPCollectionType); ok && expectedCollection.Kind == "array" &&
				bashPPTypeText(actualCollection.Element) == bashPPTypeText(expectedCollection.Element) {
				inferredToArray = true
			}
		}
	}
	if expected != nil && lit.LitType != nil && !inferredToArray && !r.bashPPTypeAssignable(typ, expected) {
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
		fixed, err = r.bashPPArrayLength(collection.Length.Value)
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
	if inferredToArray {
		expectedCollection := r.bashPPUnderlyingType(expected).(*syntax.BashPPCollectionType)
		want, err := r.bashPPArrayLength(expectedCollection.Length.Value)
		if err != nil || length != want {
			return nil, nil, fmt.Errorf("BASHPP-ECOLLECTION-LENGTH: cannot use inferred array length %d as %s", length, bashPPTypeText(expected))
		}
		meta.kind = "array"
		meta.typ = expected
		length = want
	}
	out := make([]any, length)
	meta.sequence = make([]*bashPPCollectionMeta, length)
	for i := range out {
		out[i], meta.sequence[i] = r.bashPPZeroValue(collection.Element)
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
		if len(decl.typeParams) > 0 || len(x.TypeArgs) > 0 {
			if err := r.bashPPValidateNamedTypeArgs(x); err != nil {
				return err
			}
			return r.bashPPValidateCollectionType(r.bashPPInstantiateNamedType(x))
		}
		if _, ok := decl.typeExpr.(*syntax.BashPPCollectionType); ok {
			return r.bashPPValidateCollectionType(decl.typeExpr)
		}
		if decl.underlying == "struct" {
			for _, field := range decl.fields {
				if err := r.bashPPValidateValueType(field.FieldTypeExpr, make(map[string]bool)); err != nil {
					return err
				}
			}
			return nil
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
			n, err := r.bashPPArrayLength(x.Length.Value)
			if err != nil || n < 0 {
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
	case *syntax.BashPPPointerType:
		return r.bashPPValidatePointerType(x)
	case *syntax.BashPPTypeParamType:
		return nil
	}
	return fmt.Errorf("BASHPP-ECOLLECTION-TYPE: unsupported collection type %s", bashPPTypeText(typ))
}

func (r *Runner) bashPPCollectionZero(typ syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta) {
	switch x := typ.(type) {
	case *syntax.BashPPNamedType:
		if shape, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPCollectionType); ok {
			value, meta := r.bashPPCollectionZero(shape)
			if meta != nil {
				meta.typ = typ
			}
			return value, meta
		}
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
			return nil, meta
		}
		length := 0
		if x.Kind == "array" {
			length, _ = r.bashPPArrayLength(x.Length.Value)
		} else {
			return nil, meta
		}
		values := make([]any, length)
		meta.sequence = make([]*bashPPCollectionMeta, length)
		for i := range values {
			values[i], meta.sequence[i] = r.bashPPZeroValue(x.Element)
		}
		return values, meta
	case *syntax.BashPPPointerType:
		return nil, bashPPPointerMeta(x)
	}
	return nil, nil
}

func (r *Runner) bashPPArrayLength(text string) (int, error) {
	if n, err := strconv.Atoi(text); err == nil {
		return n, nil
	}
	expr, err := goparser.ParseExpr(text)
	if err != nil {
		return 0, err
	}
	value, ok := r.bashPPEvalConstIntExpr(expr)
	if !ok || value.Kind() != constant.Int {
		return 0, fmt.Errorf("not an integer constant")
	}
	n, exact := constant.Int64Val(value)
	if !exact || int64(int(n)) != n {
		return 0, fmt.Errorf("integer constant overflows int")
	}
	return int(n), nil
}

func (r *Runner) bashPPEvalConstIntExpr(expr goast.Expr) (value constant.Value, ok bool) {
	defer func() {
		if recover() != nil {
			value, ok = nil, false
		}
	}()
	switch x := expr.(type) {
	case *goast.BasicLit:
		if x.Kind != gotoken.INT {
			return nil, false
		}
		v := constant.MakeFromLiteral(x.Value, gotoken.INT, 0)
		return v, v.Kind() == constant.Int
	case *goast.Ident:
		cell := r.bashPPScope.lookup(x.Name)
		if cell == nil || !cell.constant {
			return nil, false
		}
		v := bashPPScalarFromString(cell.vr.String()).value
		return v, v != nil && v.Kind() == constant.Int
	case *goast.ParenExpr:
		return r.bashPPEvalConstIntExpr(x.X)
	case *goast.UnaryExpr:
		v, ok := r.bashPPEvalConstIntExpr(x.X)
		if !ok {
			return nil, false
		}
		switch x.Op {
		case gotoken.ADD, gotoken.SUB, gotoken.XOR:
			return constant.UnaryOp(x.Op, v, 0), true
		}
	case *goast.BinaryExpr:
		left, lok := r.bashPPEvalConstIntExpr(x.X)
		right, rok := r.bashPPEvalConstIntExpr(x.Y)
		if !lok || !rok {
			return nil, false
		}
		if x.Op == gotoken.SHL || x.Op == gotoken.SHR {
			shift, exact := constant.Uint64Val(right)
			if !exact {
				return nil, false
			}
			return constant.Shift(left, x.Op, uint(shift)), true
		}
		switch x.Op {
		case gotoken.ADD, gotoken.SUB, gotoken.MUL, gotoken.QUO, gotoken.REM,
			gotoken.AND, gotoken.OR, gotoken.XOR, gotoken.AND_NOT:
			if (x.Op == gotoken.QUO || x.Op == gotoken.REM) && constant.Sign(right) == 0 {
				return nil, false
			}
			return constant.BinaryOp(left, x.Op, right), true
		}
	}
	return nil, false
}

func (r *Runner) bashPPEvalElement(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, error) {
	if _, pointer := r.bashPPPointerType(expected); pointer {
		return r.bashPPEvalTypedValue(expr, expected)
	}
	if _, deref := expr.(*syntax.BashPPDerefExpr); deref {
		return r.bashPPEvalTypedValue(expr, expected)
	}
	if lit, ok := expr.(*syntax.BashPPCompositeLit); ok {
		return r.bashPPEvalComposite(lit, expected)
	}
	if _, indexed := expr.(*syntax.BashPPIndexExpr); indexed {
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
	if _, sliced := expr.(*syntax.BashPPSliceExpr); sliced {
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return nil, nil, err
		}
		if err := r.bashPPCheckTypedValue(value, meta, expected); err != nil {
			return nil, nil, err
		}
		return value, meta, nil
	}
	if _, selected := expr.(*syntax.BashPPSelectorExpr); selected {
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

func (r *Runner) bashPPSliceBound(expr syntax.BashPPExpr, fallback int) (int, error) {
	if expr == nil {
		return fallback, nil
	}
	v, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		return 0, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: %v", err)
	}
	n, ok := constant.Int64Val(v.value)
	if !ok || int64(int(n)) != n {
		return 0, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: bound must be an integer")
	}
	return int(n), nil
}

func (r *Runner) bashPPSliceBounds(expr *syntax.BashPPSliceExpr, length, capacity int, sliceOperand bool) (int, int, int, error) {
	low, err := r.bashPPSliceBound(expr.Low, 0)
	if err != nil {
		return 0, 0, 0, err
	}
	high, err := r.bashPPSliceBound(expr.High, length)
	if err != nil {
		return 0, 0, 0, err
	}
	max := capacity
	if expr.SecondColon.IsValid() {
		max, err = r.bashPPSliceBound(expr.Max, capacity)
		if err != nil {
			return 0, 0, 0, err
		}
	}
	highLimit := length
	if sliceOperand {
		highLimit = capacity
	}
	if low < 0 || high < low || high > highLimit || max < high || max > capacity {
		if expr.SecondColon.IsValid() {
			return 0, 0, 0, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: slice bounds out of range [%d:%d:%d] with length %d and capacity %d", low, high, max, length, capacity)
		}
		return 0, 0, 0, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: slice bounds out of range [%d:%d] with length %d", low, high, length)
	}
	if !expr.SecondColon.IsValid() {
		max = capacity
	}
	return low, high, max, nil
}

func (r *Runner) bashPPCollectionRead(index *syntax.BashPPIndexExpr) (any, *bashPPCollectionMeta, error) {
	return r.bashPPReadExpr(index)
}

func bashPPCollectionRoot(expr syntax.BashPPExpr) (string, bool) {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return bashPPCollectionRoot(x.X)
	case *syntax.BashPPDerefExpr:
		return bashPPCollectionRoot(x.X)
	case *syntax.BashPPIdent:
		return x.Name.Value, true
	case *syntax.BashPPIndexExpr:
		return bashPPCollectionRoot(x.X)
	case *syntax.BashPPSliceExpr:
		return bashPPCollectionRoot(x.X)
	case *syntax.BashPPSelectorExpr:
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
	case *syntax.BashPPSliceExpr:
		low, high, max := "", "", ""
		if x.Low != nil {
			low = bashPPExprText(x.Low)
		}
		if x.High != nil {
			high = bashPPExprText(x.High)
		}
		if x.Max != nil {
			max = bashPPExprText(x.Max)
		}
		if x.SecondColon.IsValid() {
			return bashPPExprText(x.X) + "[" + low + ":" + high + ":" + max + "]"
		}
		return bashPPExprText(x.X) + "[" + low + ":" + high + "]"
	case *syntax.BashPPSelectorExpr:
		return bashPPExprText(x.X) + "." + x.Sel.Value
	}
	return "?"
}

func (r *Runner) bashPPCollectionAssign(target *syntax.BashPPIndexExpr, rhs syntax.BashPPExpr) {
	root, ok := bashPPCollectionRoot(target)
	cell := r.bashPPScope.lookup(root)
	rootMeta := bashPPCellMeta(cell)
	if !ok || cell == nil || rootMeta == nil {
		r.errf("BASHPP-ECOLLECTION-ASSIGN: indexed target is not a collection\n")
		r.exit = exitStatus{code: 2}
		return
	}
	path := strings.TrimPrefix(bashPPExprText(target), root)
	if cell.object != nil && cell.object.readonly {
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
		parent, meta = cell.vr.Obj, rootMeta
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
	typ, ok := r.bashPPUnderlyingType(meta.typ).(*syntax.BashPPCollectionType)
	if !ok {
		r.errf("BASHPP-ECOLLECTION-ASSIGN: indexed target is not a collection\n")
		r.exit = exitStatus{code: 2}
		return
	}
	value, child, err := r.bashPPEvalElement(rhs, typ.Element)
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return
	}
	if meta.kind == "map" {
		if parent == nil {
			r.errf("BASHPP-ENIL-MAP: assignment to nil map\n")
			r.exit = exitStatus{code: 2}
			return
		}
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
