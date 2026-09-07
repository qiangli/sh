// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"go/constant"
	"go/token"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func (r *Runner) bashPPUpdate(stmt *syntax.BashPPUpdate) {
	if stmt.Target == nil || stmt.Value == nil {
		r.bashPPUpdateError(stmt.Pos(), "FORM", "unsupported compound assignment form")
		return
	}
	if !bashPPCompoundUpdateOp(stmt.Op.Value) {
		r.bashPPUpdateError(stmt.Op.Pos(), "OP", "unsupported compound assignment operator "+stmt.Op.Value)
		return
	}
	r.bashPPApplyUpdate(stmt.Target, strings.TrimSuffix(stmt.Op.Value, "="), stmt.Value, stmt.Op.Pos())
}

func bashPPCompoundUpdateOp(op string) bool {
	switch op {
	case "+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<=", ">>=", "&^=":
		return true
	}
	return false
}

func (r *Runner) bashPPApplyUpdate(target syntax.BashPPExpr, op string, rhs syntax.BashPPExpr, pos syntax.Pos) {
	if !r.objectsEnabled() || r.bashPPScope == nil {
		r.bashPPUpdateError(pos, "DISABLED", "compound assignment evaluated with extensions disabled")
		return
	}
	if index, ok := target.(*syntax.BashPPIndexExpr); ok {
		if collection, ok := r.bashPPUnderlyingType(r.bashPPExprScalarType(index.X)).(*syntax.BashPPCollectionType); ok && collection.Kind == "map" {
			r.bashPPApplyMapUpdate(index, collection, op, rhs, pos)
			return
		}
	}
	ptr, err := r.bashPPAddress(target)
	if err != nil {
		r.bashPPUpdateError(target.Pos(), "TARGET", err.Error())
		return
	}
	if ptr == nil {
		r.bashPPUpdateError(target.Pos(), "TARGET", "nil assignment target")
		return
	}
	current, meta, expected, err := ptr.read()
	if err != nil || meta != nil {
		if err == nil {
			err = fmt.Errorf("target is not scalar")
		}
		r.bashPPUpdateError(target.Pos(), "TYPE", err.Error())
		return
	}
	var left bashPPScalar
	if len(ptr.path) == 0 && !ptr.target.pointer && ptr.target.vr.Kind == expand.String {
		left = r.bashPPScalarFromCell(ptr.target)
	} else {
		left, err = r.bashPPUpdateScalar(current, expected)
	}
	if err != nil {
		r.bashPPUpdateError(target.Pos(), "TYPE", err.Error())
		return
	}
	right, err := r.bashPPEvalScalarExpr(rhs)
	if err != nil {
		r.bashPPUpdateError(rhs.Pos(), "RHS", err.Error())
		return
	}
	value, kind, err := r.bashPPUpdateResult(op, left, right)
	if err != nil {
		r.bashPPUpdateError(pos, "OP", err.Error())
		return
	}
	if err := r.bashPPWriteUpdatePointer(ptr, value, kind); err != nil {
		r.bashPPUpdateError(target.Pos(), "WRITE", err.Error())
		return
	}
	r.exit.clear()
}

// Map indices are the one Go assignment target which is assignable but not
// addressable. Evaluate the map and key once, retain the candidate until the
// operation succeeds, and only then commit it to the map slot.
func (r *Runner) bashPPApplyMapUpdate(target *syntax.BashPPIndexExpr, collection *syntax.BashPPCollectionType, op string, rhs syntax.BashPPExpr, pos syntax.Pos) {
	root, ok := bashPPCollectionRoot(target)
	if !ok {
		r.bashPPUpdateError(target.Pos(), "TARGET", "map index has no assignable root")
		return
	}
	cell := r.bashPPScope.lookup(root)
	if cell == nil {
		r.bashPPUpdateError(target.Pos(), "TARGET", "undefined map target "+root)
		return
	}
	if cell.constant || cell.vr.ReadOnly || cell.object != nil && cell.object.readonly {
		r.bashPPUpdateError(target.Pos(), "WRITE", "BASHPP-EREADONLY-MUTATION: cannot mutate readonly map")
		return
	}
	parent, meta, err := r.bashPPReadExpr(target.X)
	if err != nil {
		r.bashPPUpdateError(target.Pos(), "TARGET", err.Error())
		return
	}
	if parent == nil {
		r.bashPPUpdateError(target.Pos(), "WRITE", "BASHPP-ENIL-MAP: assignment to nil map")
		return
	}
	mapping, ok := parent.(map[string]any)
	if !ok || meta == nil || meta.kind != "map" {
		r.bashPPUpdateError(target.Pos(), "TYPE", "target is not map storage")
		return
	}
	key, _, err := r.bashPPEvalElement(target.Index, collection.Key)
	if err != nil {
		r.bashPPUpdateError(target.Index.Pos(), "TARGET", err.Error())
		return
	}
	canonical := fmt.Sprint(key)
	current, found := mapping[canonical]
	child := meta.mapping[canonical]
	if !found {
		current, child = r.bashPPZeroValue(collection.Element)
	}
	if child != nil {
		r.bashPPUpdateError(target.Pos(), "TYPE", "target is not scalar")
		return
	}
	left, err := r.bashPPUpdateScalar(current, collection.Element)
	if err != nil {
		r.bashPPUpdateError(target.Pos(), "TYPE", err.Error())
		return
	}
	right, err := r.bashPPEvalScalarExpr(rhs)
	if err != nil {
		r.bashPPUpdateError(rhs.Pos(), "RHS", err.Error())
		return
	}
	value, _, err := r.bashPPUpdateResult(op, left, right)
	if err != nil {
		r.bashPPUpdateError(pos, "OP", err.Error())
		return
	}
	mapping[canonical] = value
	meta.mapping[canonical] = nil
	r.exit.clear()
}

func (r *Runner) bashPPUpdateResult(op string, left, right bashPPScalar) (any, constant.Kind, error) {
	if op == "/" && left.runtime && left.value.Kind() == constant.Float &&
		(right.value.Kind() == constant.Int || right.value.Kind() == constant.Float) && constant.Sign(right.value) == 0 {
		compatible := left.typ == "" && right.typ == "" || left.typ != "" && (right.typ == "" || right.typ == left.typ)
		if compatible && left.typ != "" {
			underlying, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: left.typ}}).(*syntax.BashPPNamedType)
			compatible = ok && (underlying.Name.Value == "float32" || underlying.Name.Value == "float64")
			if compatible && right.typ == "" {
				compatible = r.bashPPValidateUntypedScalarOperand(right.value, left.typ) == nil
			}
		}
		if compatible {
			return nil, constant.Unknown, fmt.Errorf("BASHPP-EUPDATE-NONFINITE: runtime floating-point division by zero is unsupported by the scalar carrier")
		}
	}
	result, err := r.bashPPBinaryScalar(bashPPOpToken(op), left, right)
	if err != nil {
		return nil, constant.Unknown, err
	}
	return bashPPUpdateGoValue(result.value), result.value.Kind(), nil
}

func (r *Runner) bashPPUpdateScalar(value any, typ syntax.BashPPTypeExpr) (bashPPScalar, error) {
	out := bashPPScalar{typ: bashPPTypeText(typ), runtime: true}
	if named, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPNamedType); ok {
		text := fmt.Sprint(value)
		switch {
		case named.Name.Value == "string":
			out.value = constant.MakeString(text)
			return out, nil
		case named.Name.Value == "bool":
			if boolean, ok := value.(bool); ok {
				out.value = constant.MakeBool(boolean)
				return out, nil
			}
		case bashPPIntegerType(named.Name.Value):
			out.value = constant.MakeFromLiteral(text, token.INT, 0)
			if out.value.Kind() == constant.Int {
				return out, nil
			}
		case named.Name.Value == "float32" || named.Name.Value == "float64":
			out.value = constant.MakeFromLiteral(text, token.FLOAT, 0)
			if out.value.Kind() == constant.Float {
				return out, nil
			}
		}
	}
	switch value := value.(type) {
	case string:
		out.value = constant.MakeString(value)
	case bool:
		out.value = constant.MakeBool(value)
	case int:
		out.value = constant.MakeInt64(int64(value))
	case float64:
		out.value = constant.MakeFloat64(value)
	default:
		return bashPPScalar{}, fmt.Errorf("value of type %T is not scalar", value)
	}
	return out, nil
}

func bashPPUpdateGoValue(value constant.Value) any {
	switch value.Kind() {
	case constant.String:
		return constant.StringVal(value)
	case constant.Bool:
		return constant.BoolVal(value)
	case constant.Int:
		if i, ok := constant.Int64Val(value); ok {
			return int(i)
		}
		return value.ExactString()
	case constant.Float:
		f, _ := constant.Float64Val(value)
		return f
	}
	return bashPPScalarString(value)
}

func (r *Runner) bashPPWriteUpdatePointer(ptr *bashPPPointer, value any, kind constant.Kind) error {
	if ptr.target.object != nil && ptr.target.object.readonly {
		return fmt.Errorf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q", ptr.target.object.owner)
	}
	if ptr.target.constant || ptr.target.vr.ReadOnly {
		return fmt.Errorf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly or constant value")
	}
	if len(ptr.path) == 0 {
		ptr.target.vr.Set = true
		ptr.target.vr.Kind = expand.String
		ptr.target.vr.Str = fmt.Sprint(value)
		ptr.target.vr.Obj = nil
		ptr.target.vr.List, ptr.target.vr.Map = nil, nil
		ptr.target.vr.ListMap, ptr.target.vr.ListSet = nil, nil
		ptr.target.scalarKind = kind
		return nil
	}
	parent, parentMeta, _, err := ptr.readParent()
	if err != nil {
		return err
	}
	last := ptr.path[len(ptr.path)-1]
	if last.field != "" {
		mapping, ok := parent.(map[string]any)
		if !ok {
			return fmt.Errorf("selector target storage is no longer a struct")
		}
		mapping[last.field] = value
		if parentMeta != nil {
			parentMeta.mapping[last.field] = nil
		}
		return nil
	}
	sequence, ok := parent.([]any)
	if !ok || last.index < 0 || last.index >= len(sequence) {
		return fmt.Errorf("index target storage is no longer addressable")
	}
	sequence[last.index] = value
	if parentMeta != nil {
		parentMeta.sequence[last.index] = nil
	}
	return nil
}

func (r *Runner) bashPPUpdateError(pos syntax.Pos, kind, message string) {
	r.errf("%sBASHPP-EUPDATE-%s: %s\n", r.bashErrPrefix(pos), kind, message)
	r.exit = exitStatus{code: 2}
}
