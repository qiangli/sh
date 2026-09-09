package interp

// Sprint: #118; Story: #52; Story-ID: d564bada90bb
import (
	"fmt"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// goSourceExpectedCell applies the assignment context without discarding the
// dynamic type of a concrete value assigned to an interface. Untyped nil has
// no declaration type until that context is known.
func (r *Runner) goSourceExpectedCell(cell *bashPPCell, expected syntax.BashPPTypeExpr) (*bashPPCell, error) {
	if !r.bashPPGoSource || cell == nil || expected == nil {
		return cell, nil
	}
	cell = bashPPCopyAssignmentCell(cell)
	if cell.declType == nil && cell.interfaceValue != nil && cell.interfaceValue.nilIface {
		if _, iface := r.bashPPInterfaceType(expected); iface {
			cell.declType = expected
			return cell, nil
		}
		shape := r.bashPPUnderlyingType(expected)
		switch t := shape.(type) {
		case *syntax.BashPPPointerType, *syntax.BashPPFuncType, *syntax.BashPPChanType:
		case *syntax.BashPPCollectionType:
			if t.Kind != "slice" && t.Kind != "map" {
				return nil, fmt.Errorf("Go nil is not assignable to %s", bashPPTypeText(expected))
			}
		default:
			return nil, fmt.Errorf("Go nil is not assignable to %s", bashPPTypeText(expected))
		}
		value, meta := r.bashPPZeroValue(expected)
		cell = &bashPPCell{declType: expected}
		bashPPStoreCellValue(cell, value, meta)
		return cell, nil
	}
	if err := r.bashPPBindInterfaceParam(cell, expected); err != nil {
		return nil, err
	}
	return cell, nil
}

// bashPPNilPointerConversion reports the pointer type of a typed nil written as
// a conversion, such as (*T)(nil). The pointer paths recognise the bare nil
// ident but not the BashPPConvertExpr the frontend builds for the conversion,
// so it otherwise reaches the generic structured read-back default and is
// refused as BASHPP-ESELECTOR-EXPR -- which is why `[]*T{nil, (*T)(nil)}`
// could not even be constructed, let alone ranged over. Returning the target
// type lets the value evaluate to a nil pointer of that type while keeping the
// conversion's own spelling available for the assignability check.
func (r *Runner) bashPPNilPointerConversion(expr syntax.BashPPExpr) (syntax.BashPPTypeExpr, bool) {
	if !r.bashPPGoSource {
		return nil, false
	}
	for {
		paren, ok := expr.(*syntax.BashPPParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	conversion, ok := expr.(*syntax.BashPPConvertExpr)
	if !ok || !goSourceNilLiteral(conversion.X) {
		return nil, false
	}
	target := r.bashPPConvertTarget(conversion)
	if _, pointer := r.bashPPPointerType(target); !pointer {
		return nil, false
	}
	return target, true
}

func goSourceNilLiteral(expr syntax.BashPPExpr) bool {
	if paren, ok := expr.(*syntax.BashPPParenExpr); ok {
		return goSourceNilLiteral(paren.X)
	}
	id, ok := expr.(*syntax.BashPPIdent)
	return ok && id.Name.Value == "nil"
}
func (r *Runner) goSourceNilValueCell(expr syntax.BashPPExpr) (*bashPPCell, bool, error) {
	if !r.bashPPGoSource {
		return nil, false, nil
	}
	literal := goSourceNilLiteral(expr)
	conversion, convert := expr.(*syntax.BashPPConvertExpr)
	if !literal && (!convert || !goSourceNilLiteral(conversion.X)) {
		return nil, false, nil
	}
	cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String}, interfaceValue: &bashPPInterfaceValue{nilIface: true}}
	if literal {
		return cell, true, nil
	}
	typed, err := r.goSourceExpectedCell(cell, r.bashPPConvertTarget(conversion))
	return typed, true, err
}

// Interface equality compares dynamic identity before the represented value.
// A typed nil pointer is not a nil interface, even though its pointer is nil.
func (r *Runner) goSourceInterfaceEqual(left, right bashPPComparableValue) (bool, bool, error) {
	if !r.bashPPGoSource || left.nilLiteral || right.nilLiteral {
		return false, false, nil
	}
	li, lok := bashPPComparablePayload(left.value, left.meta).(*bashPPInterfaceValue)
	ri, rok := bashPPComparablePayload(right.value, right.meta).(*bashPPInterfaceValue)
	if !lok && !rok {
		return false, false, nil
	}
	if !lok || !rok {
		return false, true, fmt.Errorf("Go interface comparison requires represented dynamic values")
	}
	ln, rn := li == nil || li.nilIface, ri == nil || ri.nilIface
	if ln || rn {
		return ln && rn, true, nil
	}
	if r.goSourceDynamicTypeIdentity(li.dynamic) != r.goSourceDynamicTypeIdentity(ri.dynamic) {
		return false, true, nil
	}
	if li.cell == nil || ri.cell == nil {
		return false, true, fmt.Errorf("Go interface lacks dynamic storage")
	}
	if li.cell.pointer && ri.cell.pointer {
		return bashPPPointerEqual(li.cell.pointerValue, ri.cell.pointerValue), true, nil
	}
	comparable := func(iv *bashPPInterfaceValue) (any, *bashPPCollectionMeta) {
		if iv.cell.pointer {
			return iv.cell.pointerValue, bashPPPointerMeta(iv.dynamic)
		}
		if iv.cell.vr.Kind == expand.Object {
			return iv.cell.vr.Obj, bashPPCellMeta(iv.cell)
		}
		return bashPPScalarAny(r.bashPPScalarFromCell(iv.cell).value), nil
	}
	lv, lm := comparable(li)
	rv, rm := comparable(ri)
	equal, err := bashPPCompareValues(lv, lm, false, rv, rm, false)
	return equal, true, err
}

func (r *Runner) goSourceDynamicTypeIdentity(typ syntax.BashPPTypeExpr) string {
	typ = r.bashPPCanonicalAssignableType(typ)
	switch t := typ.(type) {
	case *syntax.BashPPNamedType:
		if t.Name.Value == "byte" {
			return "uint8"
		}
		if t.Name.Value == "rune" {
			return "int32"
		}
	case *syntax.BashPPPointerType:
		return "*" + r.goSourceDynamicTypeIdentity(t.Element)
	case *syntax.BashPPCollectionType:
		if t.Kind == "map" {
			return "map[" + r.goSourceDynamicTypeIdentity(t.Key) + "]" + r.goSourceDynamicTypeIdentity(t.Element)
		}
		length := ""
		if t.Length != nil {
			length = t.Length.Value
		}
		return "[" + length + "]" + r.goSourceDynamicTypeIdentity(t.Element)
	}
	return bashPPTypeText(typ)
}

// goSourceNilableType reports whether the Go zero value of typ is nil, which is
// what decides whether an untyped nil literal may initialise it. Only pointer,
// interface, func, chan, slice and map shapes qualify; struct, array and scalar
// fields must keep rejecting nil instead of coercing it to a scalar.
func (r *Runner) goSourceNilableType(typ syntax.BashPPTypeExpr) bool {
	if typ == nil {
		return false
	}
	if _, iface := r.bashPPInterfaceType(typ); iface {
		return true
	}
	switch shape := r.bashPPUnderlyingType(typ).(type) {
	case *syntax.BashPPPointerType, *syntax.BashPPFuncType, *syntax.BashPPChanType:
		return true
	case *syntax.BashPPCollectionType:
		return shape.Kind == "slice" || shape.Kind == "map"
	}
	return false
}

// goSourceNilElement materialises an untyped nil literal, or a typed nil
// conversion, in the aggregate element representation of expected. Aggregate
// storage carries (value, meta) rather than a cell, so unlike a variable or a
// call argument the nil has to be built for the field type directly: the scalar
// fallback these paths otherwise reach reports BASHPP-EEXPR-NIL, and coercing
// nil to a scalar there would silently turn a nil field into "".
func (r *Runner) goSourceNilElement(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, bool, error) {
	if !r.bashPPGoSource || expected == nil || r.bashPPNativeType(expected) {
		return nil, nil, false, nil
	}
	literal := goSourceNilLiteral(expr)
	conversion, convert := expr.(*syntax.BashPPConvertExpr)
	typed := convert && goSourceNilLiteral(conversion.X)
	if !literal && !typed {
		return nil, nil, false, nil
	}
	if !r.goSourceNilableType(expected) {
		return nil, nil, true, fmt.Errorf("BASHPP-EASSIGN-MISMATCH: cannot use nil as %s value", bashPPTypeText(expected))
	}
	// An interface field keeps the dynamic identity of whatever nil it is
	// given: untyped nil leaves the interface itself nil, while (*int)(nil)
	// stores a non-nil interface holding a nil *int.
	if _, iface := r.bashPPInterfaceType(expected); iface {
		value, meta, err := r.bashPPEvalTypedValue(expr, expected)
		return value, meta, true, err
	}
	if typed {
		target := r.bashPPConvertTarget(conversion)
		if target == nil || !r.bashPPTypeAssignable(target, expected) {
			return nil, nil, true, fmt.Errorf("BASHPP-EASSIGN-MISMATCH: cannot use %s as %s", bashPPTypeText(target), bashPPTypeText(expected))
		}
	}
	value, meta := r.bashPPZeroValue(expected)
	return value, meta, true, nil
}

// goSourceInterfaceElement reports whether an aggregate element or struct field
// whose declared type is an interface should box its initialiser rather than
// fall through to the scalar path. A []any table entry is boxed exactly like an
// interface variable would be, so the element keeps its dynamic type instead of
// being rejected as a bare scalar.
func (r *Runner) goSourceInterfaceElement(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (bool, error) {
	if !r.bashPPGoSource || expected == nil || r.bashPPNativeType(expected) {
		return false, nil
	}
	iface, ok := r.bashPPInterfaceType(expected)
	if !ok {
		return false, nil
	}
	if r.bashPPInterfaceHasTypeTerms(iface, make(map[*syntax.BashPPInterfaceType]bool)) {
		return false, nil
	}
	// Composite literals already build their own value and meta for the
	// declared element type; boxing them here would lose that storage.
	if _, composite := expr.(*syntax.BashPPCompositeLit); composite {
		return false, nil
	}
	return true, nil
}

// A nil function/channel still carries its declared type when stored as an
// aggregate element. The descriptor denotes no live native handle or session.
func (r *Runner) goSourceNilCallableOrChannel(typ syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, bool) {
	if !r.bashPPGoSource {
		return nil, nil, false
	}
	switch r.bashPPUnderlyingType(typ).(type) {
	case *syntax.BashPPFuncType, *syntax.BashPPChanType:
		value := &bashPPBridgeValue{Kind: "nil", Type: bashPPBridgeTypeText(r.bashPPCanonicalAssignableType(typ))}
		return value, &bashPPCollectionMeta{kind: "native", typ: typ}, true
	}
	return nil, nil, false
}

func goSourceNativeComparable(value bashPPComparableValue) (bashPPBridgeValue, bool) {
	if value.nilLiteral {
		return bashPPBridgeValue{Kind: "nil"}, true
	}
	if v, ok := value.value.(*bashPPBridgeValue); ok && v != nil {
		return *v, true
	}
	return bashPPBridgeValue{}, false
}
