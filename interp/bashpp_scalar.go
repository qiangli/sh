// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"math"
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
func (r *Runner) bashPPEvalScalarExpr(expr syntax.BashPPExpr) (result bashPPScalar, failure error) {
	defer func() {
		if r.bashPPGoSource && failure != nil && !errors.Is(failure, errBashPPScalarInterrupted) && expr != nil {
			var positioned *goSourceError
			if !errors.As(failure, &positioned) {
				failure = &goSourceError{prefix: r.bashErrPrefix(expr.Pos()), err: failure}
			}
		}
	}()
	if value, handled, err := r.bashPPTestingScalar(expr); handled {
		return value, err
	}
	if value, handled, err := r.bashPPBridgeScalar(expr); handled {
		return value, err
	}
	switch x := expr.(type) {
	case *syntax.BashPPBasicLit:
		if x.Kind == "IMAG" && !r.bashPPGoSource {
			return bashPPScalar{}, fmt.Errorf("BASHPP-ECOMPLEX-UNSUPPORTED: complex values require Go source")
		}
		value, err := bashPPBasicScalar(x)
		if r.bashPPGoSource && x.Kind == "CHAR" {
			value.typ = "rune"
		}
		return value, err
	case *syntax.BashPPCall:
		if v, handled, err := r.bashPPComplexBuiltin(x); handled {
			return v, err
		}
		if x.CalleeExpr != nil {
			if r.bashPPGoSource {
				return r.bashPPScalarFuncCall(x)
			}
			return bashPPScalar{}, fmt.Errorf("gosource: computed call runtime is not implemented")
		}
		if len(x.Fun) == 0 {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-FORM: unsupported scalar call")
		}
		// A selector callee is a method value — `v.Abs()`, `p.q.M()` — which
		// the callable lookup resolves against the receiver's type. Only the
		// bare `len`/`cap` spellings are the length builtins.
		if len(x.Fun) > 1 || (x.Fun[0].Value != "len" && x.Fun[0].Value != "cap") {
			return r.bashPPScalarFuncCall(x)
		}
		name := x.Fun[0].Value
		if r.bashPPFuncs[name] != nil || (r.bashPPScope != nil && r.bashPPScope.lookup(name) != nil) {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CALL: scalar %s requires the unshadowed builtin", name)
		}
		args := make([]bashPPBuiltinArg, len(x.Args))
		for i := range x.Args {
			var err error
			args[i], err = r.goSourceBuiltinArg(x, i)
			if err != nil {
				return bashPPScalar{}, err
			}
		}
		cell, err := r.bashPPBuiltinLength(name, x, args)
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPScalarFromCell(cell), nil
	case *syntax.BashPPIdent:
		if x.Name.Value == "nil" {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-NIL: nil is not a scalar")
		}
		return r.bashPPIdentScalar(x.Name.Value)
	case *syntax.BashPPParenExpr:
		return r.bashPPEvalScalarExpr(x.X)
	case *syntax.BashPPUnaryExpr:
		// A Go receive is spelled as a unary operator but is a channel
		// operation, not arithmetic. See bashpp_chan_value.go.
		if value, handled, err := r.bashPPGoReceiveScalar(x); handled {
			return value, err
		}
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
		if op == token.LAND || op == token.LOR {
			if left.value.Kind() != constant.Bool {
				return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: logical operand must be boolean")
			}
			if known, boolean := r.bashPPBooleanExprShape(x.Y); known && !boolean {
				return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: logical operand must be boolean")
			}
			truth := constant.BoolVal(left.value)
			if op == token.LAND && !truth || op == token.LOR && truth {
				return left, nil
			}
		}
		right, err := r.bashPPEvalScalarExpr(x.Y)
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPBinaryScalar(bashPPOpToken(x.Op.Value), left, right)
	case *syntax.BashPPTypeAssertExpr:
		// `fmt.Println(i.(string))`: a one-result assertion used as a value.
		// The comma-ok spelling has its own statement forms; here a failure is
		// Go's panic, which surfaces as this expression's error.
		if x.TypeToken != nil {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EASSERT-TYPE: .(type) is only valid in a type switch")
		}
		_, source, err := r.bashPPTypeAssert(x, false)
		if err != nil {
			return bashPPScalar{}, err
		}
		if source == nil {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: asserted value is not a scalar")
		}
		return r.bashPPScalarFromCell(source), nil
	case *syntax.BashPPConvertExpr:
		// `string(bs)` reads a byte or rune slice, not a scalar; see
		// bashPPConvertCollectionScalar in bashpp_collection_convert.go. It
		// reports false for every conversion whose operand is already scalar,
		// which keeps the named-scalar path below unchanged.
		if scalar, handled, err := r.bashPPConvertCollectionScalar(x); handled {
			return scalar, err
		}
		v, err := r.bashPPEvalScalarExpr(x.X)
		if err != nil {
			return bashPPScalar{}, err
		}
		// `IteratorFunc[int](it)`: a function value converted to a named
		// function type keeps its handle and takes the name, which is what
		// its methods are then resolved on.
		if target := r.bashPPConvertTarget(x); target != nil && v.value.Kind() == constant.String {
			if _, ok := r.bashPPUnderlyingType(target).(*syntax.BashPPFuncType); ok {
				if _, closure := r.bashPPClosure(constant.StringVal(v.value)); closure {
					return bashPPScalar{value: v.value, typ: bashPPTypeText(target), runtime: true}, nil
				}
			}
		}
		return r.bashPPConvertNamedScalar(x.ConvType.Value, v)
	case *syntax.BashPPIndexExpr:
		// Strings are scalar values, not collection objects. Go indexing is by
		// byte, so keep it in the scalar evaluator and leave other index shapes
		// to the structured reader below.
		stringOperand := x.GoString
		switch x.X.(type) {
		case *syntax.BashPPBasicLit, *syntax.BashPPIdent, *syntax.BashPPParenExpr:
			stringOperand = true
		default:
			if typ := r.bashPPExprScalarType(x.X); typ != nil {
				stringOperand = bashPPTypeText(r.bashPPUnderlyingType(typ)) == "string"
			}
		}
		if stringOperand {
			base, err := r.bashPPEvalScalarExpr(x.X)
			if err != nil {
				return r.bashPPScalarPath(expr)
			}
			if base.value.Kind() != constant.String {
				return r.bashPPScalarPath(expr)
			}
			text := constant.StringVal(base.value)
			index, err := r.bashPPCollectionIndex(x.Index)
			if err != nil {
				return bashPPScalar{}, err
			}
			if index < 0 || index >= len(text) {
				return bashPPScalar{}, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for length %d", index, len(text))
			}
			return bashPPScalar{value: constant.MakeUint64(uint64(text[index])), typ: "uint8", runtime: true}, nil
		}
		return r.bashPPScalarPath(expr)
	case *syntax.BashPPSliceExpr:
		if r.bashPPGoSource && !x.GoString {
			return r.bashPPScalarPath(expr)
		}
		base, err := r.bashPPEvalScalarExpr(x.X)
		if err != nil || base.value.Kind() != constant.String {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: sliced value is not a string")
		}
		if x.Max != nil {
			return bashPPScalar{}, fmt.Errorf("BASHPP-ECOLLECTION-SLICE: three-index slicing is not defined on strings")
		}
		text := constant.StringVal(base.value)
		low, high := 0, len(text)
		if x.Low != nil {
			low, err = r.bashPPCollectionIndex(x.Low)
			if err != nil {
				return bashPPScalar{}, err
			}
		}
		if x.High != nil {
			high, err = r.bashPPCollectionIndex(x.High)
			if err != nil {
				return bashPPScalar{}, err
			}
		}
		if low < 0 || high < low || high > len(text) {
			return bashPPScalar{}, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: slice [%d:%d] out of bounds for length %d", low, high, len(text))
		}
		return bashPPScalar{value: constant.MakeString(text[low:high]), typ: "string", runtime: true}, nil
	case *syntax.BashPPSelectorExpr, *syntax.BashPPDerefExpr:
		return r.bashPPScalarPath(expr)
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-FORM: unsupported scalar expression %T", expr)
}

func (r *Runner) bashPPScalarPath(expr syntax.BashPPExpr) (bashPPScalar, error) {
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
	case int64:
		return bashPPScalar{value: constant.MakeInt64(value), typ: typ, runtime: true}, nil
	case float64:
		return bashPPScalar{value: constant.MakeFloat64(value), typ: typ, runtime: true}, nil
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: indexed value is not a scalar")
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
			if cell.scalarKind == constant.String && cell.typeName == "untyped string" {
				return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}
			}
			typ, _ := bashPPScalarNamedType(cell.typeName)
			return typ
		}
		if cell.scalarKind == constant.String {
			return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}
		}
	case *syntax.BashPPBasicLit:
		if x.Kind == "STRING" {
			return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}
		}
	case *syntax.BashPPDerefExpr:
		if pointer, ok := r.bashPPUnderlyingType(r.bashPPExprScalarType(x.X)).(*syntax.BashPPPointerType); ok {
			return pointer.Element
		}
	case *syntax.BashPPIndexExpr:
		base := r.bashPPUnderlyingType(r.bashPPExprScalarType(x.X))
		if collection, ok := base.(*syntax.BashPPCollectionType); ok {
			return collection.Element
		}
		if named, ok := base.(*syntax.BashPPNamedType); ok && named.Name.Value == "string" {
			return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "uint8"}}
		}
	case *syntax.BashPPSliceExpr:
		if named, ok := r.bashPPUnderlyingType(r.bashPPExprScalarType(x.X)).(*syntax.BashPPNamedType); ok && named.Name.Value == "string" {
			return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}
		}
	case *syntax.BashPPSelectorExpr:
		parent := r.bashPPExprScalarType(x.X)
		if sel := r.bashPPResolveField(parent, x.Sel.Value); !sel.ambiguous && len(sel.edges) > 0 {
			return sel.fieldType
		}
	}
	return nil
}

func bashPPComparableFallback(err error) bool {
	return strings.HasPrefix(err.Error(), "BASHPP-ECOMPARE-SCALAR:")
}

func bashPPBasicScalar(x *syntax.BashPPBasicLit) (bashPPScalar, error) {
	kind := map[string]token.Token{"INT": token.INT, "IMAG": token.IMAG, "FLOAT": token.FLOAT, "CHAR": token.CHAR, "STRING": token.STRING}[x.Kind]
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
	if r.bashPPScope != nil {
		if cell := r.bashPPScope.lookup(name); cell != nil {
			return r.bashPPScalarFromCell(cell), nil
		}
	}
	return bashPPScalarFromString(vr.String()), nil
}

// bashPPScalarFromCell reconstructs a scalar using lexical provenance before
// considering its rendered shell text. Quoted "2" and "true" values must not
// become numbers or booleans merely because their storage is textual.
func (r *Runner) bashPPScalarFromCell(cell *bashPPCell) bashPPScalar {
	if r.bashPPGoSource && cell.constant && cell.exactScalar != nil {
		value := bashPPScalar{value: cell.exactScalar}
		if named, ok := cell.declType.(*syntax.BashPPNamedType); ok {
			value.typ = named.Name.Value
		}
		return value
	}
	text := cell.vr.String()
	value := bashPPScalar{}
	switch cell.scalarKind {
	case constant.String:
		value.value = constant.MakeString(text)
	case constant.Bool:
		value.value = constant.MakeBool(text == "true")
	case constant.Int:
		value.value = constant.MakeFromLiteral(text, token.INT, 0)
	case constant.Complex:
		value.value = bashPPParseComplex(text)
	case constant.Float:
		value.value = constant.MakeFromLiteral(text, token.FLOAT, 0)
	default:
		if named, ok := r.bashPPUnderlyingType(cell.declType).(*syntax.BashPPNamedType); ok {
			switch named.Name.Value {
			case "string":
				value.value = constant.MakeString(text)
			case "bool":
				value.value = constant.MakeBool(text == "true")
			default:
				if bashPPIntegerType(named.Name.Value) {
					value.value = constant.MakeFromLiteral(text, token.INT, 0)
				} else if r.bashPPGoSource && (named.Name.Value == "complex64" || named.Name.Value == "complex128") {
					value.value = bashPPParseComplex(text)
				} else if named.Name.Value == "float32" || named.Name.Value == "float64" {
					value.value = constant.MakeFromLiteral(text, token.FLOAT, 0)
				}
			}
		}
		if value.value == nil || value.value.Kind() == constant.Unknown {
			value = bashPPScalarFromString(text)
		}
	}
	if r.bashPPGoSource && cell.scalarKind == constant.Float && (value.value == nil || value.value.Kind() == constant.Unknown) {
		if parts := strings.Split(text, "/"); len(parts) == 2 {
			numerator := constant.MakeFromLiteral(parts[0], token.FLOAT, 0)
			denominator := constant.MakeFromLiteral(parts[1], token.FLOAT, 0)
			if numerator.Kind() != constant.Unknown && denominator.Kind() != constant.Unknown && constant.Sign(denominator) != 0 {
				value.value = constant.BinaryOp(numerator, token.QUO, denominator)
			}
		}
	}
	value.runtime = !cell.constant
	switch {
	case cell.typeName != "":
		value.typ = cell.typeName
	case cell.declType != nil:
		if named, ok := cell.declType.(*syntax.BashPPNamedType); ok {
			value.typ = named.Name.Value
		}
	}
	return value
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
		if x.value.Kind() != constant.Int && x.value.Kind() != constant.Float && !(r.bashPPGoSource && x.value.Kind() == constant.Complex) {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator %s not defined on %s", op, x.value.Kind())
		}
		if op == token.XOR && x.value.Kind() != constant.Int {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator ^ requires integer operand")
		}
		precision := uint(0)
		if r.bashPPGoSource && op == token.XOR {
			typ := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: x.typ}})
			if named, ok := typ.(*syntax.BashPPNamedType); ok && named.Name != nil && bashPPIntegerType(named.Name.Value) {
				if width, signed := bashPPIntegerWidth(named.Name.Value); !signed {
					precision = uint(width)
				}
			}
		}
		return r.bashPPTypedScalarResult(constant.UnaryOp(op, x.value, precision), x.typ, x.runtime)
	case token.NOT:
		if x.value.Kind() != constant.Bool {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator ! requires boolean operand")
		}
		return r.bashPPTypedScalarResult(constant.MakeBool(!constant.BoolVal(x.value)), x.typ, x.runtime)
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: unsupported unary operator %s", op)
}

func (r *Runner) bashPPBinaryScalar(op token.Token, left, right bashPPScalar) (bashPPScalar, error) {
	if r.bashPPGoSource {
		canonical := func(name string) string {
			if _, declared := r.bashPPTypes[name]; declared {
				return name
			}
			switch name {
			case "byte":
				return "uint8"
			case "rune":
				return "int32"
			}
			return name
		}
		left.typ, right.typ = canonical(left.typ), canonical(right.typ)
	}
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
	if r.bashPPGoSource && (left.runtime || right.runtime) {
		if named, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: resultType}}).(*syntax.BashPPNamedType); ok && (named.Name.Value == "float32" || named.Name.Value == "float64") {
			var err error
			left, err = r.bashPPConvertScalar(named.Name.Value, left)
			if err != nil {
				return bashPPScalar{}, err
			}
			right, err = r.bashPPConvertScalar(named.Name.Value, right)
			if err != nil {
				return bashPPScalar{}, err
			}
		}
	}
	if v, handled, err := r.bashPPComplexRuntimeOp(op, left, right, resultType); handled {
		return v, err
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
		arithmeticOp := op
		if r.bashPPGoSource && op == token.QUO && left.value.Kind() == constant.Int && right.value.Kind() == constant.Int {
			integer := resultType == ""
			if typ, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: resultType}}).(*syntax.BashPPNamedType); ok {
				integer = integer || bashPPIntegerType(typ.Name.Value)
			}
			if integer {
				arithmeticOp = token.QUO_ASSIGN
			}
		}
		value, err := bashPPBinaryOp(left.value, arithmeticOp, right.value)
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
		runtime := left.runtime
		if r.bashPPGoSource {
			runtime = runtime || right.runtime
		}
		if r.bashPPGoSource && runtime {
			if named, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: left.typ}}).(*syntax.BashPPNamedType); ok && bashPPIntegerType(named.Name.Value) {
				bits, _ := bashPPIntegerWidth(named.Name.Value)
				if shift >= uint64(bits) {
					value := constant.MakeInt64(0)
					if op == token.SHR && constant.Sign(left.value) < 0 {
						value = constant.MakeInt64(-1)
					}
					return r.bashPPTypedScalarResult(value, left.typ, true)
				}
			}
		}
		value, err := bashPPShiftScalar(left.value, op, uint(shift))
		if err != nil {
			return bashPPScalar{}, err
		}
		return r.bashPPTypedScalarResult(value, left.typ, runtime)
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
	if r.bashPPGoSource && (named.Name.Value == "float32" || named.Name.Value == "float64") {
		out, err := r.bashPPConvertScalar(named.Name.Value, bashPPScalar{value: value, runtime: runtime})
		out.typ = typ
		return out, err
	}
	if r.bashPPGoSource && (named.Name.Value == "complex64" || named.Name.Value == "complex128") {
		out, err := r.bashPPConvertComplex(named.Name.Value, bashPPScalar{value: value, runtime: runtime})
		out.typ = typ
		return out, err
	}
	if err := r.bashPPValidateUntypedScalarOperand(value, typ); err != nil {
		return bashPPScalar{}, err
	}
	return bashPPScalar{value: value, typ: typ, runtime: runtime}, nil
}

func bashPPIntegerWidth(typ string) (bits int, signed bool) {
	bits, signed = strconv.IntSize, true
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
	return bits, signed
}

func bashPPWrapInteger(typ string, value constant.Value) constant.Value {
	bits, signed := bashPPIntegerWidth(typ)
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
	if r.bashPPGoSource && (r.bashPPNativeExpr(left) || r.bashPPNativeExpr(right)) {
		return r.bashPPNativeCompare(left, op, right)
	}
	lv, err := r.bashPPComparableExpr(left)
	if err != nil {
		return false, err
	}
	rv, err := r.bashPPComparableExpr(right)
	if err != nil {
		return false, err
	}
	if r.bashPPGoSource {
		lv.value = bashPPComparablePayload(lv.value, lv.meta)
		rv.value = bashPPComparablePayload(rv.value, rv.meta)
		// Indexed aggregate reads can carry native typed nils even when the
		// containing aggregate is local. Reuse the evaluated operands.
		if l, lok := goSourceNativeComparable(lv); lok {
			if rr, rok := goSourceNativeComparable(rv); rok {
				return r.bashPPNativeCompareValues(l, op, rr)
			}
		}
	}
	if equal, handled, err := r.goSourceInterfaceEqual(lv, rv); handled {
		if op == token.NEQ {
			equal = !equal
		}
		return equal, err
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
			if cell.interfaceValue != nil {
				return bashPPComparableValue{value: cell.interfaceValue, meta: &bashPPCollectionMeta{kind: "interface", typ: cell.declType}}, nil
			}
			if cell.pointer {
				return bashPPComparableValue{value: cell.pointerValue, meta: bashPPPointerMeta(cell.declType)}, nil
			}
			if r.bashPPGoSource {
				if native, ok := cell.vr.Obj.(*bashPPBridgeValue); ok && native != nil && native.Kind == "nil" {
					if _, ok := r.bashPPUnderlyingType(cell.declType).(*syntax.BashPPFuncType); ok {
						return bashPPComparableValue{meta: &bashPPCollectionMeta{kind: "func", typ: cell.declType}}, nil
					}
				}
			}
			if cell.vr.Kind == expand.Object {
				return bashPPComparableValue{value: cell.vr.Obj, meta: bashPPCellMeta(cell)}, nil
			}
			if r.bashPPGoSource {
				if _, ok := r.bashPPUnderlyingType(cell.declType).(*syntax.BashPPFuncType); ok {
					var value any = cell.vr.Str
					if cell.vr.Str == "" || cell.vr.Str == "nil" {
						value = nil
					}
					return bashPPComparableValue{value: value, meta: &bashPPCollectionMeta{kind: "func", typ: cell.declType}}, nil
				}
			}
		}
		return bashPPComparableValue{}, fmt.Errorf("BASHPP-ECOMPARE-SCALAR: scalar")
	case *syntax.BashPPAddressExpr, *syntax.BashPPNewExpr:
		ptr, err := r.bashPPPointerExprValue(expr)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		return bashPPComparableValue{value: ptr, meta: bashPPPointerMeta(&syntax.BashPPPointerType{Element: ptr.elem})}, nil
	case *syntax.BashPPCall:
		if !r.bashPPGoSource {
			return bashPPComparableValue{}, fmt.Errorf("BASHPP-ECOMPARE-SCALAR: scalar")
		}
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		return bashPPComparableValue{value: value, meta: meta}, nil
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
	case "channel":
		return bashPPChannelElementEqual(left, leftMeta, right, rightMeta), nil
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
	return meta != nil && (meta.kind == "slice" || meta.kind == "map" || meta.kind == "interface" || meta.kind == "channel" || meta.kind == "func")
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

// bashPPComparablePayload recovers the value that decides an interface's
// identity. A variable carries its *bashPPInterfaceValue on the cell, but an
// interface stored inside a slice, map or struct keeps that identity on the
// element meta and leaves the JSON-shaped payload as the printable value.
// Reading such an element back therefore yields "" rather than the interface,
// which made an untyped nil element compare unequal to nil while the same nil
// held in a variable compared equal. Recovering it here keeps the nil
// interface and a typed nil interface distinguishable on read-back.
func bashPPComparablePayload(value any, meta *bashPPCollectionMeta) any {
	if meta == nil || meta.kind != "interface" || meta.interfaceValue == nil {
		return value
	}
	if _, ok := value.(*bashPPInterfaceValue); ok {
		return value
	}
	return meta.interfaceValue
}

func bashPPNilComparableValue(value any) bool {
	if value == nil {
		return true
	}
	if ptr, ok := value.(*bashPPPointer); ok {
		return ptr == nil
	}
	if iface, ok := value.(*bashPPInterfaceValue); ok {
		return iface == nil || iface.nilIface
	}
	// A dependency-owned channel stored in a collection is its handle, and the
	// dependency reports a nil one as the nil value rather than as a handle.
	if native, ok := value.(*bashPPBridgeValue); ok {
		return native == nil || native.Kind == "nil"
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
	case "complex64", "complex128":
		return r.bashPPConvertComplex(typ, x)
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
			if typ == "float32" {
				value, _ := constant.Float32Val(x.value)
				if math.IsInf(float64(value), 0) {
					return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: constant %s overflows %s", x.value, typ)
				}
			} else {
				value, _ := constant.Float64Val(x.value)
				if math.IsInf(value, 0) {
					return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: constant %s overflows %s", x.value, typ)
				}
			}
			value := constant.ToFloat(x.value)
			if r.bashPPGoSource {
				if typ == "float32" {
					v, _ := constant.Float32Val(x.value)
					value = constant.MakeFloat64(float64(v))
				} else {
					v, _ := constant.Float64Val(x.value)
					value = constant.MakeFloat64(v)
				}
			}
			return bashPPScalar{value: value, typ: typ, runtime: x.runtime}, nil
		}
	default:
		if bashPPIntegerType(typ) && (x.value.Kind() == constant.Int || x.value.Kind() == constant.Float) {
			integer := constant.ToInt(x.value)
			if r.bashPPGoSource && x.runtime {
				if x.value.Kind() == constant.Float {
					// Runtime floating conversions truncate toward zero. Do not
					// round an exact untyped constant or route it through float64.
					n, d := constant.Num(x.value), constant.Denom(x.value)
					integer = constant.BinaryOp(n, token.QUO_ASSIGN, d)
				}
				integer = bashPPWrapInteger(typ, integer)
			}
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
	case constant.Complex:
		return strconv.FormatComplex(bashPPComplexNumber(v), 'g', -1, 128)
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
	case "bool", "byte", "complex64", "complex128", "float32", "float64", "int", "int8", "int16",
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

// Preserve static operand diagnostics without evaluating a skipped logical RHS.
func (r *Runner) bashPPBooleanExprShape(expr syntax.BashPPExpr) (known, boolean bool) {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.bashPPBooleanExprShape(x.X)
	case *syntax.BashPPBasicLit:
		return true, false
	case *syntax.BashPPIdent:
		if x.Name.Value == "true" || x.Name.Value == "false" {
			return true, true
		}
		if r.bashPPScope != nil {
			if cell := r.bashPPScope.lookup(x.Name.Value); cell != nil && cell.vr.Kind != expand.Object && !cell.pointer && cell.interfaceValue == nil {
				return true, r.bashPPScalarFromCell(cell).value.Kind() == constant.Bool
			}
		}
	case *syntax.BashPPUnaryExpr:
		return true, x.Op.Value == "!"
	case *syntax.BashPPBinaryExpr:
		switch x.Op.Value {
		case "==", "!=", "<", "<=", ">", ">=", "&&", "||":
			return true, true
		}
		return true, false
	case *syntax.BashPPCall:
		if len(x.Fun) == 1 {
			if name := x.Fun[0].Value; name == "len" || name == "cap" {
				return true, false
			}
			if fn, ok := r.bashPPLookupFunc(x); ok {
				resultTypes := bashppResultTypeExprs(fn.results())
				if len(resultTypes) != 1 {
					return true, false
				}
				underlying, ok := r.bashPPUnderlyingType(resultTypes[0]).(*syntax.BashPPNamedType)
				return true, ok && underlying.Name.Value == "bool"
			}
		}
		return false, false
	}
	return false, false
}

// bashPPScalarFuncCall consumes the positioned call node at the point selected
// by the scalar evaluator. In particular a skipped logical operand never
// evaluates arguments or enters the function frame.
func (r *Runner) bashPPScalarFuncCall(call *syntax.BashPPCall) (bashPPScalar, error) {
	fn, ok := r.bashPPLookupFunc(call)
	if !ok {
		name := "computed function"
		if len(call.Fun) > 0 {
			name = call.Fun[0].Value
		}
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-UNDEFINED: undefined callable %s", name)
	}
	if bashppResultCount(fn.results()) != 1 {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CALL: scalar call requires one result")
	}
	var args []string
	if call.ArgExprs != nil {
		var err error
		if args, ok, err = r.bashPPTypedCallArgs(call, fn); err != nil {
			return bashPPScalar{}, err
		}
	} else {
		args, ok = r.bashPPCallValues(call, fn)
	}
	if !ok {
		return bashPPScalar{}, errBashPPScalarInterrupted
	}

	previous := r.bashPPResultCells
	defer func() { r.bashPPResultCells = previous }()
	failure := r.bashPPShortFailureSeq
	values := r.bashPPInvoke(r.ectx, fn, args)
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != failure || len(values) != 1 {
		return bashPPScalar{}, errBashPPScalarInterrupted
	}
	if len(r.bashPPResultCells) != 1 {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CALL: scalar result metadata missing")
	}
	result := r.bashPPScalarFromCell(r.bashPPResultCells[0])
	if result.value == nil || result.value.Kind() == constant.Unknown {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: call result is not a scalar")
	}
	return result, nil
}

// A call may have already reported its failure, or may be unwinding through
// panic/exit. Scalar statement consumers must retain that state unchanged.
var errBashPPScalarInterrupted = errors.New("scalar call interrupted")
