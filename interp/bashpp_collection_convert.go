// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"go/constant"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Conversions whose operand or result is a collection rather than a scalar —
// `[]byte(s)`, `[]rune(s)`, and `string(bs)` — cannot go through the scalar
// converter, which only knows how to name a builtin type and reports
// "cannot convert String to []byte".
//
// The Go-source front end records the structured conversion target on
// BashPPConvertExpr.ConvTypeExpr, so the target is read from the AST the front
// end already produced rather than by re-parsing ConvType's spelling or
// rewriting the original source.

// bashPPConvertTarget resolves a conversion's target type, preferring the
// structured form and falling back to the legacy spelling for the named types
// that Bash++ conversions carried before ConvTypeExpr existed.
func (r *Runner) bashPPConvertTarget(x *syntax.BashPPConvertExpr) syntax.BashPPTypeExpr {
	if x.ConvTypeExpr != nil {
		return x.ConvTypeExpr
	}
	if x.ConvType != nil && syntax.BashPPValidIdent(x.ConvType.Value) {
		return &syntax.BashPPNamedType{Name: x.ConvType}
	}
	return nil
}

// bashPPByteOrRuneSlice reports whether typ is a slice whose elements are the
// byte or rune spellings a string converts to and from. The answer is the
// element's own kind, since a byte slice converts the string's bytes and a rune
// slice its code points.
func (r *Runner) bashPPByteOrRuneSlice(typ syntax.BashPPTypeExpr) (string, bool) {
	if typ == nil {
		return "", false
	}
	collection, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPCollectionType)
	if !ok || collection.Kind != "slice" {
		return "", false
	}
	named, ok := r.bashPPUnderlyingType(collection.Element).(*syntax.BashPPNamedType)
	if !ok {
		return "", false
	}
	switch named.Name.Value {
	case "byte", "uint8":
		return "byte", true
	case "rune", "int32":
		return "rune", true
	}
	return "", false
}

// bashPPCollectionOperand reads an expression that is expected to hold
// structured storage, whether it is named outright or reached through a path.
// A scalar operand reports false and is left to the scalar evaluator.
func (r *Runner) bashPPCollectionOperand(expr syntax.BashPPExpr) (any, *bashPPCollectionMeta, bool) {
	if paren, ok := expr.(*syntax.BashPPParenExpr); ok {
		return r.bashPPCollectionOperand(paren.X)
	}
	if lit, ok := expr.(*syntax.BashPPCompositeLit); ok {
		if lit.LitType == nil {
			return nil, nil, false
		}
		value, meta, err := r.bashPPEvalComposite(lit, lit.LitType)
		if err != nil || meta == nil {
			return nil, nil, false
		}
		return value, meta, true
	}
	if id, ok := expr.(*syntax.BashPPIdent); ok {
		if r.bashPPScope == nil {
			return nil, nil, false
		}
		cell := r.bashPPScope.lookup(id.Name.Value)
		if cell == nil || cell.vr.Kind != expand.Object {
			return nil, nil, false
		}
		meta := bashPPCellMeta(cell)
		if meta == nil {
			return nil, nil, false
		}
		return cell.vr.Obj, meta, true
	}
	return r.bashPPStructuredBridgeRead(expr)
}

// bashPPElementInt recovers the integer a byte or rune element holds. Elements
// arrive as the interpreter's own int, but one read back through a bridge value
// or an exact scalar can still be spelled as text.
func bashPPElementInt(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case string:
		if n, err := constant.Int64Val(constant.MakeFromLiteral(v, 0, 0)); err {
			return int(n), true
		}
	}
	return 0, false
}

// bashPPConvertToCollection implements `[]byte(s)` and `[]rune(s)`. The third
// result reports whether this conversion is the collection-producing kind, so a
// scalar conversion falls through untouched; an error is only returned once the
// conversion has been claimed.
func (r *Runner) bashPPConvertToCollection(x *syntax.BashPPConvertExpr) (any, *bashPPCollectionMeta, bool, error) {
	target := r.bashPPConvertTarget(x)
	elemKind, ok := r.bashPPByteOrRuneSlice(target)
	if !ok {
		return nil, nil, false, nil
	}
	// `[]byte(bs)` on a slice operand is an identity conversion; the payload is
	// shared, exactly as Go shares it.
	if value, meta, ok := r.bashPPCollectionOperand(x.X); ok {
		if _, ok := r.bashPPByteOrRuneSlice(meta.typ); !ok {
			return nil, nil, true, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert %s to %s", bashPPTypeText(meta.typ), bashPPTypeText(target))
		}
		converted := *meta
		converted.typ = target
		return value, &converted, true, nil
	}
	scalar, err := r.bashPPEvalScalarExpr(x.X)
	if err != nil {
		return nil, nil, true, err
	}
	if scalar.value.Kind() != constant.String {
		return nil, nil, true, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert %s to %s", scalar.value.Kind(), bashPPTypeText(target))
	}
	text := constant.StringVal(scalar.value)
	var units []int
	if elemKind == "byte" {
		for _, b := range []byte(text) {
			units = append(units, int(b))
		}
	} else {
		for _, ru := range text {
			units = append(units, int(ru))
		}
	}
	// Keep Classic's established []any growth. GoSource carries the original
	// constant-string distinction rather than guessing from the runtime value.
	if !r.bashPPGoSource {
		elements := []any{}
		for _, unit := range units {
			elements = append(elements, unit)
		}
		return elements, &bashPPCollectionMeta{kind: "slice", typ: target, sequence: make([]*bashPPCollectionMeta, len(elements))}, true, nil
	}
	shape, _ := r.bashPPUnderlyingType(target).(*syntax.BashPPCollectionType)
	capacity := bashPPStringConversionCapacity(text, elemKind, x.GoStringConstant)
	elements, sequence := r.bashPPConvertedStringStorage(shape.Element, len(units), capacity)
	for i, unit := range units {
		elements[i] = unit
	}
	meta := &bashPPCollectionMeta{kind: "slice", typ: target, sequence: sequence}
	return elements, meta, true, nil
}

// bashPPConvertCollectionScalar implements `string(bs)` for a byte or rune
// slice. Like its counterpart it reports whether it claimed the conversion, so
// that `string(n)` and the other scalar conversions keep their own path.
func (r *Runner) bashPPConvertCollectionScalar(x *syntax.BashPPConvertExpr) (bashPPScalar, bool, error) {
	target := r.bashPPConvertTarget(x)
	named, ok := r.bashPPUnderlyingType(target).(*syntax.BashPPNamedType)
	if !ok || named.Name.Value != "string" {
		return bashPPScalar{}, false, nil
	}
	if r.bashPPGoSource && r.bashPPNativeExpr(x.X) {
		value, err := r.bashPPBridgeExpr(x.X)
		if err != nil {
			return bashPPScalar{}, true, err
		}
		text, ok, err := r.bashPPNativeStringConversion(value)
		if !ok {
			return bashPPScalar{}, false, nil
		}
		if err != nil {
			return bashPPScalar{}, true, err
		}
		typ := ""
		if declared, ok := target.(*syntax.BashPPNamedType); ok {
			typ = declared.Name.Value
		}
		return bashPPScalar{value: constant.MakeString(text), typ: typ, runtime: true}, true, nil
	}
	value, meta, ok := r.bashPPCollectionOperand(x.X)
	if !ok {
		return bashPPScalar{}, false, nil
	}
	elemKind, ok := r.bashPPByteOrRuneSlice(meta.typ)
	if !ok {
		return bashPPScalar{}, true, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert %s to string", bashPPTypeText(meta.typ))
	}
	sequence, _ := value.([]any)
	var out strings.Builder
	for _, item := range sequence {
		n, ok := bashPPElementInt(item)
		if !ok {
			return bashPPScalar{}, true, fmt.Errorf("BASHPP-EEXPR-CONVERT: %s element is not an integer", elemKind)
		}
		if elemKind == "byte" {
			out.WriteByte(byte(n))
		} else {
			out.WriteRune(rune(n))
		}
	}
	typ := ""
	if declared, ok := target.(*syntax.BashPPNamedType); ok {
		typ = declared.Name.Value
	}
	return bashPPScalar{value: constant.MakeString(out.String()), typ: typ, runtime: true}, true, nil
}

func (r *Runner) bashPPNativeStringConversion(value bashPPBridgeValue) (string, bool, error) {
	elemKind := ""
	switch value.Type {
	case "[]uint8", "[]byte":
		elemKind = "byte"
	case "[]int32", "[]rune":
		elemKind = "rune"
	default:
		return "", false, nil
	}
	if value.Kind == "nil" {
		return "", true, nil
	}
	if value.Kind != "handle" {
		return "", false, nil
	}
	length, err := r.bashPPNativeLen(r.ectx, value)
	if err != nil {
		return "", true, err
	}
	var out strings.Builder
	for i := range length {
		element, err := r.bashPPNativeAccess(r.ectx, "index", value, "", bashPPBridgeValue{Kind: "int", Text: fmt.Sprint(i)})
		if err != nil {
			return "", true, err
		}
		scalar, err := element.scalar()
		if err != nil {
			return "", true, err
		}
		n, ok := constant.Int64Val(scalar.value)
		if !ok {
			return "", true, fmt.Errorf("BASHPP-EEXPR-CONVERT: %s element is not an integer", elemKind)
		}
		if elemKind == "byte" {
			out.WriteByte(byte(n))
		} else {
			out.WriteRune(rune(n))
		}
	}
	return out.String(), true, nil
}

// bashPPConvertCollectionCell wraps a collection-producing conversion in the
// cell that carries its payload, metadata and declared type, so that `[]byte(s)`
// can be bound, assigned and passed like any other slice value.
func (r *Runner) bashPPConvertCollectionCell(x *syntax.BashPPConvertExpr) (*bashPPCell, bool, error) {
	value, meta, handled, err := r.bashPPConvertToCollection(x)
	if !handled || err != nil {
		return nil, handled, err
	}
	cell := &bashPPCell{declType: meta.typ}
	bashPPStoreCellValue(cell, value, meta)
	return cell, true, nil
}
