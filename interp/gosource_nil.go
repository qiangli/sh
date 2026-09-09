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
		case *syntax.BashPPPointerType:
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
	li, lok := left.value.(*bashPPInterfaceValue)
	ri, rok := right.value.(*bashPPInterfaceValue)
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
