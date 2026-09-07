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
	left, err := r.bashPPUpdateScalar(current, expected)
	if err != nil {
		r.bashPPUpdateError(target.Pos(), "TYPE", err.Error())
		return
	}
	right, err := r.bashPPEvalScalarExpr(rhs)
	if err != nil {
		r.bashPPUpdateError(rhs.Pos(), "RHS", err.Error())
		return
	}
	result, err := r.bashPPBinaryScalar(bashPPOpToken(op), left, right)
	if err != nil {
		r.bashPPUpdateError(pos, "OP", err.Error())
		return
	}
	if err := r.bashPPWriteUpdatePointer(ptr, bashPPUpdateGoValue(result.value)); err != nil {
		r.bashPPUpdateError(target.Pos(), "WRITE", err.Error())
		return
	}
	r.exit.clear()
}

func (r *Runner) bashPPUpdateScalar(value any, typ syntax.BashPPTypeExpr) (bashPPScalar, error) {
	out := bashPPScalar{typ: bashPPTypeText(typ), runtime: true}
	if named, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPNamedType); ok && named.Name.Value == "string" {
		out.value = constant.MakeString(fmt.Sprint(value))
		return out, nil
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
		i, _ := constant.Int64Val(value)
		return int(i)
	case constant.Float:
		f, _ := constant.Float64Val(value)
		return f
	}
	return bashPPScalarString(value)
}

func (r *Runner) bashPPWriteUpdatePointer(ptr *bashPPPointer, value any) error {
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
