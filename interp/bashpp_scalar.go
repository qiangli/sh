// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"go/constant"
	"go/token"
	"strconv"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type bashPPScalar struct {
	value constant.Value
	typ   string
}

// bashPPEvalScalarExpr consumes syntax's typed tree. Parsing belongs solely
// to syntax; this package evaluates the tree it was handed.
func (r *Runner) bashPPEvalScalarExpr(expr syntax.BashPPExpr) (bashPPScalar, error) {
	switch x := expr.(type) {
	case *syntax.BashPPBasicLit:
		return bashPPBasicScalar(x)
	case *syntax.BashPPIdent:
		return r.bashPPIdentScalar(x.Name.Value)
	case *syntax.BashPPParenExpr:
		return r.bashPPEvalScalarExpr(x.X)
	case *syntax.BashPPUnaryExpr:
		v, err := r.bashPPEvalScalarExpr(x.X)
		if err != nil {
			return bashPPScalar{}, err
		}
		return bashPPUnaryScalar(bashPPOpToken(x.Op.Value), v)
	case *syntax.BashPPBinaryExpr:
		left, err := r.bashPPEvalScalarExpr(x.X)
		if err != nil {
			return bashPPScalar{}, err
		}
		right, err := r.bashPPEvalScalarExpr(x.Y)
		if err != nil {
			return bashPPScalar{}, err
		}
		return bashPPBinaryScalar(bashPPOpToken(x.Op.Value), left, right)
	case *syntax.BashPPConvertExpr:
		v, err := r.bashPPEvalScalarExpr(x.X)
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPConvertScalar(x.ConvType.Value, v)
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-FORM: unsupported scalar expression %T", expr)
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
		if cell := r.bashPPScope.lookup(name); cell != nil && cell.typeName != "" {
			value.typ = cell.typeName
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

func bashPPUnaryScalar(op token.Token, x bashPPScalar) (bashPPScalar, error) {
	switch op {
	case token.ADD, token.SUB, token.XOR:
		if x.value.Kind() != constant.Int && x.value.Kind() != constant.Float {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator %s not defined on %s", op, x.value.Kind())
		}
		if op == token.XOR && x.value.Kind() != constant.Int {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator ^ requires integer operand")
		}
		return bashPPScalar{value: constant.UnaryOp(op, x.value, 0)}, nil
	case token.NOT:
		if x.value.Kind() != constant.Bool {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator ! requires boolean operand")
		}
		return bashPPScalar{value: constant.MakeBool(!constant.BoolVal(x.value))}, nil
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: unsupported unary operator %s", op)
}

func bashPPBinaryScalar(op token.Token, left, right bashPPScalar) (bashPPScalar, error) {
	switch op {
	case token.LAND, token.LOR:
		if left.value.Kind() != constant.Bool || right.value.Kind() != constant.Bool {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator %s requires boolean operands", op)
		}
		l, r := constant.BoolVal(left.value), constant.BoolVal(right.value)
		return bashPPScalar{value: constant.MakeBool(op == token.LAND && l && r || op == token.LOR && (l || r))}, nil
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
		if left.typ != "" && right.typ != "" && left.typ != right.typ {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-MISMATCH: mismatched scalar types %s and %s", left.typ, right.typ)
		}
		value, err := bashPPBinaryOp(left.value, op, right.value)
		if err != nil {
			return bashPPScalar{}, err
		}
		return bashPPScalar{value: value}, nil
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
		return bashPPScalar{value: value}, nil
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: unsupported binary operator %s", op)
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
			return bashPPScalar{value: x.value, typ: typ}, nil
		case constant.Int:
			n, ok := constant.Int64Val(x.value)
			if !ok {
				return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert %s to string", x.value)
			}
			return bashPPScalar{value: constant.MakeString(string(rune(n))), typ: typ}, nil
		}
	case "bool":
		if x.value.Kind() == constant.Bool {
			return bashPPScalar{value: x.value, typ: typ}, nil
		}
	case "float32", "float64":
		if x.value.Kind() == constant.Int || x.value.Kind() == constant.Float {
			return bashPPScalar{value: constant.ToFloat(x.value), typ: typ}, nil
		}
	default:
		if bashPPIntegerType(typ) && x.value.Kind() == constant.Int {
			if !bashPPIntegerRepresentable(typ, x.value) {
				return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: constant %s overflows %s", x.value, typ)
			}
			return bashPPScalar{value: constant.ToInt(x.value), typ: typ}, nil
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
