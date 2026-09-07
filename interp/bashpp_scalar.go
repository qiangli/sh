// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"go/constant"
	"go/token"
	"math/big"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type bashPPScalar struct {
	value   constant.Value
	typ     string
	runtime bool
}

// bashPPEvalScalarExpr consumes syntax's typed tree. Parsing belongs solely
// to syntax; this package evaluates the tree it was handed.
func (r *Runner) bashPPEvalScalarExpr(expr syntax.BashPPExpr) (bashPPScalar, error) {
	switch x := expr.(type) {
	case *syntax.BashPPBasicLit:
		return bashPPBasicScalar(x)
	case *syntax.BashPPIdent:
		if x.Name.Value == "nil" {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-NIL: nil is not a scalar")
		}
		return r.bashPPIdentScalar(x.Name.Value)
	case *syntax.BashPPParenExpr:
		return r.bashPPEvalScalarExpr(x.X)
	case *syntax.BashPPUnaryExpr:
		v, err := r.bashPPEvalScalarExpr(x.X)
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPUnaryScalar(bashPPOpToken(x.Op.Value), v)
	case *syntax.BashPPBinaryExpr:
		op := bashPPOpToken(x.Op.Value)
		if op == token.EQL || op == token.NEQ {
			ok, err := r.bashPPCompareExpr(x.X, op, x.Y)
			if err == nil {
				return bashPPScalar{value: constant.MakeBool(ok)}, nil
			}
			if !bashPPComparableFallback(err) {
				return bashPPScalar{}, err
			}
		}
		left, err := r.bashPPEvalScalarExpr(x.X)
		if err != nil {
			return bashPPScalar{}, err
		}
		right, err := r.bashPPEvalScalarExpr(x.Y)
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPBinaryScalar(bashPPOpToken(x.Op.Value), left, right)
	case *syntax.BashPPConvertExpr:
		v, err := r.bashPPEvalScalarExpr(x.X)
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPConvertScalar(x.ConvType.Value, v)
	case *syntax.BashPPIndexExpr, *syntax.BashPPSelectorExpr:
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return bashPPScalar{}, err
		}
		if meta != nil {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: indexed value is not a scalar")
		}
		typ := ""
		if scalarType := r.bashPPExprScalarType(expr); scalarType != nil {
			if named, ok := scalarType.(*syntax.BashPPNamedType); ok {
				typ = named.Name.Value
			}
		}
		switch value := value.(type) {
		case string:
			return bashPPScalar{value: constant.MakeString(value), typ: typ, runtime: true}, nil
		case bool:
			return bashPPScalar{value: constant.MakeBool(value), typ: typ, runtime: true}, nil
		case int:
			return bashPPScalar{value: constant.MakeInt64(int64(value)), typ: typ, runtime: true}, nil
		case float64:
			return bashPPScalar{value: constant.MakeFloat64(value), typ: typ, runtime: true}, nil
		}
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: indexed value is not a scalar")
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-FORM: unsupported scalar expression %T", expr)
}

// bashPPExprScalarType follows the declared shape of a path after the value
// reader has established that its result is scalar. Scalar child metadata is
// intentionally nil, so recovering []Count elements and Count struct fields
// must use the parent's type rather than the value metadata.
func (r *Runner) bashPPExprScalarType(expr syntax.BashPPExpr) syntax.BashPPTypeExpr {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.bashPPExprScalarType(x.X)
	case *syntax.BashPPIdent:
		if r.bashPPScope == nil {
			return nil
		}
		cell := r.bashPPScope.lookup(x.Name.Value)
		if cell == nil {
			return nil
		}
		if cell.declType != nil {
			return cell.declType
		}
		if meta := bashPPCellMeta(cell); meta != nil {
			return meta.typ
		}
		if cell.typeName != "" {
			return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: cell.typeName}}
		}
	case *syntax.BashPPDerefExpr:
		if pointer, ok := r.bashPPUnderlyingType(r.bashPPExprScalarType(x.X)).(*syntax.BashPPPointerType); ok {
			return pointer.Element
		}
	case *syntax.BashPPIndexExpr:
		if collection, ok := r.bashPPUnderlyingType(r.bashPPExprScalarType(x.X)).(*syntax.BashPPCollectionType); ok {
			return collection.Element
		}
	case *syntax.BashPPSelectorExpr:
		if fields, _, ok := r.bashPPStructFields(r.bashPPExprScalarType(x.X)); ok {
			if typ, found := bashPPFieldType(fields, x.Sel.Value); found {
				return typ
			}
		}
	}
	return nil
}

func bashPPComparableFallback(err error) bool {
	return strings.HasPrefix(err.Error(), "BASHPP-ECOMPARE-SCALAR:")
}

func bashPPBasicScalar(x *syntax.BashPPBasicLit) (bashPPScalar, error) {
	kind := map[string]token.Token{"INT": token.INT, "FLOAT": token.FLOAT, "CHAR": token.CHAR, "STRING": token.STRING}[x.Kind]
	v := constant.MakeFromLiteral(x.Value.Value, kind, 0)
	if v.Kind() == constant.Unknown {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-LITERAL: invalid literal %s", x.Value.Value)
	}
	return bashPPScalar{value: v}, nil
}

func (r *Runner) bashPPIdentScalar(name string) (bashPPScalar, error) {
	switch name {
	case "true":
		return bashPPScalar{value: constant.MakeBool(true)}, nil
	case "false":
		return bashPPScalar{value: constant.MakeBool(false)}, nil
	}
	vr := r.lookupVar(name)
	if !vr.IsSet() {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-UNDEFINED: undefined: %s", name)
	}
	if vr.Kind == expand.Object {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: %s is not a scalar", name)
	}
	value := bashPPScalarFromString(vr.String())
	if r.bashPPScope != nil {
		if cell := r.bashPPScope.lookup(name); cell != nil {
			value.runtime = !cell.constant
			switch {
			case cell.typeName != "":
				value.typ = cell.typeName
			case cell.declType != nil:
				if named, ok := cell.declType.(*syntax.BashPPNamedType); ok {
					value.typ = named.Name.Value
				}
			}
		}
	}
	return value, nil
}

func bashPPOpToken(op string) token.Token {
	switch op {
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "!":
		return token.NOT
	case "^":
		return token.XOR
	case "||":
		return token.LOR
	case "&&":
		return token.LAND
	case "==":
		return token.EQL
	case "!=":
		return token.NEQ
	case "<":
		return token.LSS
	case "<=":
		return token.LEQ
	case ">":
		return token.GTR
	case ">=":
		return token.GEQ
	case "|":
		return token.OR
	case "*":
		return token.MUL
	case "/":
		return token.QUO
	case "%":
		return token.REM
	case "<<":
		return token.SHL
	case ">>":
		return token.SHR
	case "&":
		return token.AND
	case "&^":
		return token.AND_NOT
	}
	return token.ILLEGAL
}

func bashPPScalarFromString(s string) bashPPScalar {
	if s == "true" {
		return bashPPScalar{value: constant.MakeBool(true)}
	}
	if s == "false" {
		return bashPPScalar{value: constant.MakeBool(false)}
	}
	if v := constant.MakeFromLiteral(s, token.INT, 0); v.Kind() != constant.Unknown {
		return bashPPScalar{value: v}
	}
	if v := constant.MakeFromLiteral(s, token.FLOAT, 0); v.Kind() != constant.Unknown {
		return bashPPScalar{value: v}
	}
	return bashPPScalar{value: constant.MakeString(s)}
}

func (r *Runner) bashPPUnaryScalar(op token.Token, x bashPPScalar) (bashPPScalar, error) {
	switch op {
	case token.ADD, token.SUB, token.XOR:
		if x.value.Kind() != constant.Int && x.value.Kind() != constant.Float {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator %s not defined on %s", op, x.value.Kind())
		}
		if op == token.XOR && x.value.Kind() != constant.Int {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator ^ requires integer operand")
		}
		return r.bashPPTypedScalarResult(constant.UnaryOp(op, x.value, 0), x.typ, x.runtime)
	case token.NOT:
		if x.value.Kind() != constant.Bool {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator ! requires boolean operand")
		}
		return r.bashPPTypedScalarResult(constant.MakeBool(!constant.BoolVal(x.value)), x.typ, x.runtime)
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: unsupported unary operator %s", op)
}

func (r *Runner) bashPPBinaryScalar(op token.Token, left, right bashPPScalar) (bashPPScalar, error) {
	resultType := left.typ
	if resultType == "" {
		resultType = right.typ
	}
	comparison := op == token.EQL || op == token.NEQ || op == token.LSS || op == token.LEQ || op == token.GTR || op == token.GEQ
	shift := op == token.SHL || op == token.SHR
	if !comparison && !shift {
		if left.typ != "" && right.typ != "" && left.typ != right.typ {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-MISMATCH: mismatched scalar types %s and %s", left.typ, right.typ)
		}
		// An untyped constant combines with a typed operand only when the
		// constant itself is representable in that type; checking merely the
		// final result would incorrectly accept expressions such as
		// `300 - tinyUint8`.
		if left.typ != "" && right.typ == "" {
			if err := r.bashPPValidateUntypedScalarOperand(right.value, left.typ); err != nil {
				return bashPPScalar{}, err
			}
		}
		if right.typ != "" && left.typ == "" {
			if err := r.bashPPValidateUntypedScalarOperand(left.value, right.typ); err != nil {
				return bashPPScalar{}, err
			}
		}
	}
	switch op {
	case token.LAND, token.LOR:
		if left.value.Kind() != constant.Bool || right.value.Kind() != constant.Bool {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator %s requires boolean operands", op)
		}
		leftBool, rightBool := constant.BoolVal(left.value), constant.BoolVal(right.value)
		return r.bashPPTypedScalarResult(constant.MakeBool(op == token.LAND && leftBool && rightBool || op == token.LOR && (leftBool || rightBool)), resultType, left.runtime || right.runtime)
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		ok, err := bashPPCompareScalar(left.value, op, right.value)
		if err != nil {
			return bashPPScalar{}, err
		}
		return bashPPScalar{value: constant.MakeBool(ok)}, nil
	case token.QUO, token.REM:
		if right.value.Kind() == constant.Int && constant.Sign(right.value) == 0 {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-DIVZERO: division by zero")
		}
		if right.value.Kind() == constant.Float {
			if f, _ := constant.Float64Val(right.value); f == 0 {
				return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-DIVZERO: division by zero")
			}
		}
		fallthrough
	case token.ADD, token.SUB, token.OR, token.XOR, token.MUL, token.AND, token.AND_NOT:
		value, err := bashPPBinaryOp(left.value, op, right.value)
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPTypedScalarResult(value, resultType, left.runtime || right.runtime)
	case token.SHL, token.SHR:
		// constant.Uint64Val panics on a non-integer, and `1 << "a"` is now
		// reachable from source, so the kind is checked before the call rather
		// than recovered after it.
		if right.value.Kind() != constant.Int {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-SHIFT: shift count must be an unsigned integer")
		}
		shift, ok := constant.Uint64Val(right.value)
		if !ok {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-SHIFT: shift count must be an unsigned integer")
		}
		value, err := bashPPShiftScalar(left.value, op, uint(shift))
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPTypedScalarResult(value, left.typ, left.runtime)
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: unsupported binary operator %s", op)
}

func (r *Runner) bashPPValidateUntypedScalarOperand(value constant.Value, typ string) error {
	underlying := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: typ}})
	named, ok := underlying.(*syntax.BashPPNamedType)
	if !ok {
		return fmt.Errorf("BASHPP-EEXPR-TYPE: %s does not have a scalar underlying type", typ)
	}
	_, err := r.bashPPConvertScalar(named.Name.Value, bashPPScalar{value: value})
	return err
}

// bashPPTypedScalarResult retains the defined operand type of a non-comparison
// operation and verifies that the exact constant carrier still fits its
// underlying scalar type. This prevents a named integer expression from being
// silently downgraded to untyped int (or retaining an impossible value).
func (r *Runner) bashPPTypedScalarResult(value constant.Value, typ string, runtime bool) (bashPPScalar, error) {
	if typ == "" {
		return bashPPScalar{value: value, runtime: runtime}, nil
	}
	underlying := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: typ}})
	named, ok := underlying.(*syntax.BashPPNamedType)
	if !ok {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-TYPE: %s does not have a scalar underlying type", typ)
	}
	if runtime && bashPPIntegerType(named.Name.Value) && value.Kind() == constant.Int {
		value = bashPPWrapInteger(named.Name.Value, value)
	}
	if err := r.bashPPValidateUntypedScalarOperand(value, typ); err != nil {
		return bashPPScalar{}, err
	}
	return bashPPScalar{value: value, typ: typ, runtime: runtime}, nil
}

func bashPPWrapInteger(typ string, value constant.Value) constant.Value {
	bits, signed := strconv.IntSize, true
	switch typ {
	case "int8":
		bits = 8
	case "int16":
		bits = 16
	case "int32", "rune":
		bits = 32
	case "int64":
		bits = 64
	case "uint8", "byte":
		bits, signed = 8, false
	case "uint16":
		bits, signed = 16, false
	case "uint32":
		bits, signed = 32, false
	case "uint64":
		bits, signed = 64, false
	case "uint", "uintptr":
		signed = false
	}
	n, ok := new(big.Int).SetString(value.ExactString(), 10)
	if !ok {
		return value
	}
	modulus := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	n.Mod(n, modulus)
	if signed && n.Bit(bits-1) != 0 {
		n.Sub(n, modulus)
	}
	return constant.MakeFromLiteral(n.String(), token.INT, 0)
}

func bashPPShiftScalar(left constant.Value, op token.Token, shift uint) (value constant.Value, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("BASHPP-EEXPR-SHIFT: shift requires integer operands")
		}
	}()
	value = constant.Shift(left, op, shift)
	if value.Kind() == constant.Unknown {
		err = fmt.Errorf("BASHPP-EEXPR-SHIFT: shift requires integer operands")
	}
	return value, err
}

func bashPPCompareScalar(left constant.Value, op token.Token, right constant.Value) (ok bool, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("BASHPP-EEXPR-OPERAND: operator %s not defined on %s and %s", op, left.Kind(), right.Kind())
		}
	}()
	return constant.Compare(left, op, right), nil
}

type bashPPComparableValue struct {
	value      any
	meta       *bashPPCollectionMeta
	nilLiteral bool
}

func (r *Runner) bashPPCompareExpr(left syntax.BashPPExpr, op token.Token, right syntax.BashPPExpr) (bool, error) {
	lv, err := r.bashPPComparableExpr(left)
	if err != nil {
		return false, err
	}
	rv, err := r.bashPPComparableExpr(right)
	if err != nil {
		return false, err
	}
	ok, err := bashPPCompareValues(lv.value, lv.meta, lv.nilLiteral, rv.value, rv.meta, rv.nilLiteral)
	if err != nil {
		return false, err
	}
	if op == token.NEQ {
		ok = !ok
	}
	return ok, nil
}

func (r *Runner) bashPPComparableExpr(expr syntax.BashPPExpr) (bashPPComparableValue, error) {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.bashPPComparableExpr(x.X)
	case *syntax.BashPPIdent:
		if x.Name.Value == "nil" {
			return bashPPComparableValue{nilLiteral: true}, nil
		}
		if cell := r.bashPPScope.lookup(x.Name.Value); cell != nil {
			if cell.pointer {
				return bashPPComparableValue{value: cell.pointerValue, meta: bashPPPointerMeta(cell.declType)}, nil
			}
			if cell.vr.Kind == expand.Object {
				return bashPPComparableValue{value: cell.vr.Obj, meta: bashPPCellMeta(cell)}, nil
			}
		}
		return bashPPComparableValue{}, fmt.Errorf("BASHPP-ECOMPARE-SCALAR: scalar")
	case *syntax.BashPPAddressExpr, *syntax.BashPPNewExpr:
		ptr, err := r.bashPPPointerExprValue(expr)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		return bashPPComparableValue{value: ptr, meta: bashPPPointerMeta(&syntax.BashPPPointerType{Element: ptr.elem})}, nil
	case *syntax.BashPPDerefExpr, *syntax.BashPPIndexExpr, *syntax.BashPPSliceExpr, *syntax.BashPPSelectorExpr:
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		return bashPPComparableValue{value: value, meta: meta}, nil
	}
	return bashPPComparableValue{}, fmt.Errorf("BASHPP-ECOMPARE-SCALAR: scalar")
}

func bashPPCompareValues(left any, leftMeta *bashPPCollectionMeta, leftNilLiteral bool, right any, rightMeta *bashPPCollectionMeta, rightNilLiteral bool) (bool, error) {
	if leftNilLiteral || rightNilLiteral {
		if leftNilLiteral && rightNilLiteral {
			return false, fmt.Errorf("BASHPP-ECOMPARE-TYPE: nil cannot be compared with nil")
		}
		value, meta := left, leftMeta
		if leftNilLiteral {
			value, meta = right, rightMeta
		}
		if bashPPPointerComparable(meta) || bashPPNilComparable(meta) {
			return bashPPNilComparableValue(value), nil
		}
		return false, fmt.Errorf("BASHPP-ECOMPARE-TYPE: value cannot be compared with nil")
	}
	if leftMeta == nil && rightMeta == nil {
		return bashPPCompareScalarAny(left, right)
	}
	if bashPPPointerComparable(leftMeta) || bashPPPointerComparable(rightMeta) {
		if !bashPPPointerComparable(leftMeta) || !bashPPPointerComparable(rightMeta) || bashPPTypeText(leftMeta.typ) != bashPPTypeText(rightMeta.typ) {
			return false, fmt.Errorf("BASHPP-ECOMPARE-TYPE: mismatched pointer comparison")
		}
		return bashPPPointerEqual(left, right), nil
	}
	if leftMeta == nil || rightMeta == nil {
		return false, fmt.Errorf("BASHPP-ECOMPARE-TYPE: mismatched comparison")
	}
	if leftMeta.kind != rightMeta.kind || bashPPTypeText(leftMeta.typ) != bashPPTypeText(rightMeta.typ) {
		return false, fmt.Errorf("BASHPP-ECOMPARE-TYPE: mismatched comparison")
	}
	switch leftMeta.kind {
	case "slice", "map":
		return false, fmt.Errorf("BASHPP-ECOMPARE-NONCOMPARABLE: %s values can only be compared to nil", leftMeta.kind)
	case "array", "inferred-array":
		leftSeq := left.([]any)
		rightSeq := right.([]any)
		if len(leftSeq) != len(rightSeq) {
			return false, nil
		}
		for i := range leftSeq {
			ok, err := bashPPCompareValues(leftSeq[i], leftMeta.sequence[i], false, rightSeq[i], rightMeta.sequence[i], false)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case "struct":
		leftMap := left.(map[string]any)
		rightMap := right.(map[string]any)
		for field, child := range leftMeta.mapping {
			ok, err := bashPPCompareValues(leftMap[field], child, false, rightMap[field], rightMeta.mapping[field], false)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("BASHPP-ECOMPARE-NONCOMPARABLE: unsupported comparison")
}

func bashPPPointerComparable(meta *bashPPCollectionMeta) bool {
	return meta != nil && meta.kind == "pointer"
}

func bashPPNilComparable(meta *bashPPCollectionMeta) bool {
	return meta != nil && (meta.kind == "slice" || meta.kind == "map")
}

func bashPPCompareScalarAny(left, right any) (bool, error) {
	switch l := left.(type) {
	case nil:
		return right == nil, nil
	case string:
		r, ok := right.(string)
		return ok && l == r, nil
	case bool:
		r, ok := right.(bool)
		return ok && l == r, nil
	case int:
		r, ok := right.(int)
		return ok && l == r, nil
	case float64:
		r, ok := right.(float64)
		return ok && l == r, nil
	}
	return false, fmt.Errorf("BASHPP-ECOMPARE-NONCOMPARABLE: unsupported scalar comparison")
}

func bashPPPointerEqual(left, right any) bool {
	lp, _ := left.(*bashPPPointer)
	rp, _ := right.(*bashPPPointer)
	if lp == nil || rp == nil {
		return lp == nil && rp == nil
	}
	if lp.target != rp.target || len(lp.path) != len(rp.path) {
		return false
	}
	for i := range lp.path {
		if lp.path[i] != rp.path[i] {
			return false
		}
	}
	return true
}

func bashPPNilComparableValue(value any) bool {
	if value == nil {
		return true
	}
	if ptr, ok := value.(*bashPPPointer); ok {
		return ptr == nil
	}
	return false
}

func bashPPBinaryOp(left constant.Value, op token.Token, right constant.Value) (value constant.Value, err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("BASHPP-EEXPR-OPERAND: operator %s not defined on %s and %s", op, left.Kind(), right.Kind())
		}
	}()
	value = constant.BinaryOp(left, op, right)
	if value.Kind() == constant.Unknown {
		err = fmt.Errorf("BASHPP-EEXPR-OPERAND: operator %s not defined on %s and %s", op, left.Kind(), right.Kind())
	}
	return value, err
}

func (r *Runner) bashPPConvertScalar(typ string, x bashPPScalar) (bashPPScalar, error) {
	switch typ {
	case "string":
		switch x.value.Kind() {
		case constant.String:
			return bashPPScalar{value: x.value, typ: typ, runtime: x.runtime}, nil
		case constant.Int:
			n, ok := constant.Int64Val(x.value)
			if !ok {
				return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert %s to string", x.value)
			}
			return bashPPScalar{value: constant.MakeString(string(rune(n))), typ: typ, runtime: x.runtime}, nil
		}
	case "bool":
		if x.value.Kind() == constant.Bool {
			return bashPPScalar{value: x.value, typ: typ, runtime: x.runtime}, nil
		}
	case "float32", "float64":
		if x.value.Kind() == constant.Int || x.value.Kind() == constant.Float {
			return bashPPScalar{value: constant.ToFloat(x.value), typ: typ, runtime: x.runtime}, nil
		}
	default:
		if bashPPIntegerType(typ) && (x.value.Kind() == constant.Int || x.value.Kind() == constant.Float) {
			integer := constant.ToInt(x.value)
			if integer.Kind() != constant.Int {
				break
			}
			if !bashPPIntegerRepresentable(typ, integer) {
				return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: constant %s overflows %s", x.value, typ)
			}
			return bashPPScalar{value: integer, typ: typ, runtime: x.runtime}, nil
		}
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert %s to %s", x.value.Kind(), typ)
}

func bashPPIntegerRepresentable(typ string, v constant.Value) bool {
	if typ == "int" {
		i, ok := constant.Int64Val(v)
		if !ok {
			return false
		}
		if strconv.IntSize == 64 {
			return true
		}
		return i >= -(int64(1)<<31) && i < int64(1)<<31
	}
	if typ == "uint" || typ == "uintptr" {
		u, ok := constant.Uint64Val(v)
		if !ok {
			return false
		}
		return strconv.IntSize == 64 || u < uint64(1)<<32
	}
	bits := 0
	unsigned := false
	switch typ {
	case "int8":
		bits = 8
	case "int16":
		bits = 16
	case "int32", "rune":
		bits = 32
	case "int64":
		bits = 64
	case "uint8", "byte":
		bits, unsigned = 8, true
	case "uint16":
		bits, unsigned = 16, true
	case "uint32":
		bits, unsigned = 32, true
	case "uint64":
		bits, unsigned = 64, true
	}
	if unsigned {
		u, ok := constant.Uint64Val(v)
		return ok && (bits == 64 || u < 1<<bits)
	}
	i, ok := constant.Int64Val(v)
	if !ok {
		return false
	}
	if bits == 64 {
		return true
	}
	return i >= -(1<<(bits-1)) && i < 1<<(bits-1)
}

func bashPPScalarString(v constant.Value) string {
	switch v.Kind() {
	case constant.String:
		return constant.StringVal(v)
	case constant.Bool:
		return strconv.FormatBool(constant.BoolVal(v))
	default:
		return v.ExactString()
	}
}

func bashPPScalarType(name string) bool {
	switch name {
	case "bool", "byte", "float32", "float64", "int", "int8", "int16",
		"int32", "int64", "rune", "string", "uint", "uint8", "uint16",
		"uint32", "uint64", "uintptr":
		return true
	}
	return false
}

func bashPPIntegerType(name string) bool {
	switch name {
	case "byte", "int", "int8", "int16", "int32", "int64", "rune",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	}
	return false
}
