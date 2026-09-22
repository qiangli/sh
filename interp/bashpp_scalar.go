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
	value        constant.Value
	typ          string
	runtime      bool
	negativeZero bool
	// nonFinite carries the runtime IEEE values that go/constant deliberately
	// cannot represent. It is only set for Go-source runtime float operations.
	nonFinite           float64
	hasNonFinite        bool
	nonFiniteComplex    complex128
	hasNonFiniteComplex bool
}

func (v bashPPScalar) kind() constant.Kind {
	if v.hasNonFiniteComplex {
		return constant.Complex
	}
	if v.value == nil {
		return constant.Unknown
	}
	return v.value.Kind()
}

// bashPPEvalScalarExpr consumes syntax's typed tree. Parsing belongs solely
// to syntax; this package evaluates the tree it was handed.
func (r *Runner) bashPPEvalScalarExpr(expr syntax.BashPPExpr) (result bashPPScalar, failure error) {
	defer func() {
		failure = r.goSourceRuntimeFaultAt(failure, expr)
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
	if value, handled, err := r.bashPPPythonValue(expr); handled {
		if err != nil {
			return bashPPScalar{}, err
		}
		return bashPPPythonScalar(value)
	}
	if call, ok := expr.(*syntax.BashPPCall); ok {
		if value, handled, err := r.goSourceUnsafeConstant(call); handled {
			return value, err
		}
	}
	localConversion := false
	if conversion, ok := expr.(*syntax.BashPPConvertExpr); ok && r.bashPPGoSource {
		if target := r.bashPPConvertTarget(conversion); target != nil {
			if named, ok := target.(*syntax.BashPPNamedType); ok && named.Name != nil {
				if _, local := r.bashPPTypes[named.Name.Value]; local {
					underlying, _ := r.bashPPUnderlyingType(target).(*syntax.BashPPNamedType)
					localConversion = underlying != nil && underlying.Name != nil &&
						(underlying.Name.Value == "complex64" || underlying.Name.Value == "complex128")
				}
			}
		}
	}
	if !localConversion {
		if value, handled, err := r.bashPPBridgeScalar(expr); handled {
			return value, err
		}
	}
	switch x := expr.(type) {
	case *syntax.BashPPBasicLit:
		if x.Kind == "IMAG" && !r.bashPPGoSource {
			return bashPPScalar{}, fmt.Errorf("BASHPP-ECOMPLEX-UNSUPPORTED: complex values require Go source")
		}
		value, err := bashPPBasicScalar(x)
		return value, err
	case *syntax.BashPPCall:
		if r.bashPPGoSource && bashPPRecoverExpr(x) && r.bashPPFuncs["recover"] == nil && (r.bashPPScope == nil || r.bashPPScope.lookup("recover") == nil) {
			iv, _ := r.bashPPRecoverInterfaceValue()
			if iv.cell == nil {
				return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-NIL: recover returned nil interface")
			}
			return r.bashPPScalarFromCell(iv.cell), nil
		}
		if v, handled, err := r.bashPPComplexBuiltin(x); handled {
			return v, err
		}
		if x.CalleeExpr != nil {
			if r.bashPPGoSource {
				return r.bashPPScalarFuncCall(x)
			}
			return bashPPScalar{}, fmt.Errorf("gosource: computed call runtime is not implemented")
		}
		// An immediately-invoked literal — `if func() bool {…}() {…}` — is a
		// call whose callee the lookup already resolves from the literal.
		if x.FuncLit != nil {
			return r.bashPPScalarFuncCall(x)
		}
		if len(x.Fun) == 0 {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-FORM: unsupported scalar call")
		}
		// A selector callee is a method value — `v.Abs()`, `p.q.M()` — which
		// the callable lookup resolves against the receiver's type. Only the
		// bare `len`/`cap`/`copy` spellings are builtins with a scalar result.
		if len(x.Fun) > 1 || (x.Fun[0].Value != "len" && x.Fun[0].Value != "cap" && x.Fun[0].Value != "copy") {
			return r.bashPPScalarFuncCall(x)
		}
		name := x.Fun[0].Value
		if r.bashPPFuncs[name] != nil || (r.bashPPScope != nil && r.bashPPScope.lookup(name) != nil) {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CALL: scalar %s requires the unshadowed builtin", name)
		}
		// `copy(dst, src)` used as a value — `if copy(s1, s2) != n` — is the
		// same mutation the statement position runs; its count is the scalar.
		if name == "copy" {
			cell, produced := r.bashPPRunValueBuiltin(name, x)
			if !produced || cell == nil {
				return bashPPScalar{}, errBashPPScalarInterrupted
			}
			return r.bashPPScalarFromCell(cell), nil
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
		if target := r.bashPPConvertTarget(x); target != nil && v.value != nil && v.value.Kind() == constant.String {
			if _, ok := r.bashPPUnderlyingType(target).(*syntax.BashPPFuncType); ok {
				if _, closure := r.bashPPClosure(constant.StringVal(v.value)); closure {
					return bashPPScalar{value: v.value, typ: bashPPTypeText(target), runtime: true}, nil
				}
			}
		}
		// A conversion to an interface type — `(J)(t)`, `I[T](x)`, an
		// anonymous `(interface{ M() })(v)` — is an interface assignment,
		// not a representation change: the checked program guarantees the
		// operand implements it, so the value keeps its dynamic identity.
		// A named pointer type — `Peano(p)` with `type Peano *Peano` — is
		// the same identity conversion with the target's name attached.
		if r.bashPPGoSource {
			if target := r.bashPPConvertTarget(x); target != nil {
				if _, iface := r.bashPPInterfaceType(target); iface {
					return v, nil
				}
				if _, pointer := r.bashPPUnderlyingType(target).(*syntax.BashPPPointerType); pointer {
					return bashPPScalar{value: v.value, typ: bashPPTypeText(target), runtime: true}, nil
				}
			}
		}
		return r.bashPPConvertNamedScalar(r.bashPPConvertTargetName(x), x.ConvTypeExpr, v)
	case *syntax.BashPPIndexExpr:
		// Strings are scalar values, not collection objects. Go indexing is by
		// byte, so keep it in the scalar evaluator and leave other index shapes
		// to the structured reader below.
		stringOperand := x.GoString
		switch operand := x.X.(type) {
		case *syntax.BashPPIdent:
			// A pointer or structured variable is never a string operand,
			// whatever its (empty) scalar spelling: `p[i]` on a pointer to
			// an array reads through p below.
			stringOperand = !r.goSourceStructuredIdent(operand)
		case *syntax.BashPPBasicLit, *syntax.BashPPParenExpr:
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
			if index.outOfBounds(len(text)) {
				if r.bashPPGoSource {
					// Indexing a string out of range is Go's recoverable runtime
					// panic, not the classic hard diagnostic.
					return bashPPScalar{}, r.bashPPSprint162CollectionBoundsPanic(x, index, len(text))
				}
				return bashPPScalar{}, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %s out of bounds for length %d", index.text, len(text))
			}
			return bashPPScalar{value: constant.MakeUint64(uint64(text[index.value])), typ: "uint8", runtime: true}, nil
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
		low, lowRuntime, err := r.bashPPStringSliceBound(x.Low, 0)
		if err != nil {
			return bashPPScalar{}, err
		}
		high, highRuntime, err := r.bashPPStringSliceBound(x.High, len(text))
		if err != nil {
			return bashPPScalar{}, err
		}
		if low.less(bashPPCollectionIndexInt(0)) || high.less(low) || high.greaterThan(len(text)) {
			// A runtime-typed bound (or a runtime string operand) faults as
			// Go's recoverable slice-bounds panic; a wholly constant invalid
			// slice is the Go-source checker's compile-time error and stays a
			// front-end diagnostic here.
			if r.bashPPGoSource && (base.runtime || lowRuntime || highRuntime) {
				return bashPPScalar{}, r.goSourceSliceBoundsPanic(x, low, high, bashPPCollectionIndexInt(0), len(text), len(text), false)
			}
			return bashPPScalar{}, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: slice [%s:%s] out of bounds for length %d", low.text, high.text, len(text))
		}
		return bashPPScalar{value: constant.MakeString(text[low.value:high.value]), typ: "string", runtime: true}, nil
	case *syntax.BashPPSelectorExpr, *syntax.BashPPDerefExpr:
		return r.bashPPScalarPath(expr)
	case *syntax.BashPPFuncLit:
		// A literal in value position — `Func(func() {})` — is the same
		// closure a declaration would bind: its handle, typed by its own
		// signature so a conversion or method lookup resolves against it.
		fn, vr := r.bashPPMakeClosure(x)
		return bashPPScalar{value: constant.MakeString(vr.Str), typ: bashPPTypeText(bashPPFuncLitType(fn.lit)), runtime: true}, nil
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-FORM: unsupported scalar expression %T", expr)
}

// bashPPReparseIntegerCarrier reconstructs an integer scalar from the decimal
// spelling a large-unsigned element is stored under. Only a well-formed
// integer literal representable in the destination integer type qualifies;
// an ordinary string element (declared string, or a named string type) is
// left as its string carrier.
func (r *Runner) bashPPReparseIntegerCarrier(typ, text string) (bashPPScalar, bool) {
	dest, ok := r.bashPPUnderlyingIntegerName(typ)
	if !ok || !bashPPCollectionIntegerText(dest, text) {
		return bashPPScalar{}, false
	}
	return bashPPScalar{value: constant.MakeFromLiteral(text, token.INT, 0), typ: typ, runtime: true}, true
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
		if r.bashPPGoSource && r.bashPPStringCarriesComplex(r.bashPPExprScalarType(expr), value) {
			return bashPPScalar{value: bashPPParseComplex(value), typ: typ, runtime: true}, nil
		}
		// A NaN or infinity reaches a float element only as its storage
		// spelling, since go/constant has no value for it.
		if r.bashPPGoSource && r.bashPPFloatTypeName(typ) {
			if special, ok := bashPPNonFiniteText(value); ok {
				return bashPPNonFiniteScalar(special, typ), nil
			}
		}
		// A large unsigned element is stored as its decimal spelling because
		// it exceeds the interpreter's signed int carrier. Reconstruct it as
		// the integer it is when the declared scalar type is integral, so
		// every downstream use (bridge, printf, comparison) sees a number.
		if r.bashPPGoSource {
			if reparsed, ok := r.bashPPReparseIntegerCarrier(typ, value); ok {
				return reparsed, nil
			}
		}
		return bashPPScalar{value: constant.MakeString(value), typ: typ, runtime: true}, nil
	case bool:
		return bashPPScalar{value: constant.MakeBool(value), typ: typ, runtime: true}, nil
	case int:
		return bashPPScalar{value: constant.MakeInt64(int64(value)), typ: typ, runtime: true}, nil
	case int64:
		return bashPPScalar{value: constant.MakeInt64(value), typ: typ, runtime: true}, nil
	case float64:
		if r.bashPPGoSource && (math.IsInf(value, 0) || math.IsNaN(value)) {
			return bashPPNonFiniteScalar(value, typ), nil
		}
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
	case *syntax.BashPPCompositeLit:
		return x.LitType
	case *syntax.BashPPAddressExpr:
		// `&v` is a pointer to v's type; a later `*&v` deref must recover v's
		// declared type, not fall back to its predeclared base. Without this the
		// named type of `*&b` is dropped and the value boxes into an interface
		// as the bare builtin, so `x.(Named)` reports the wrong dynamic type.
		if elem := r.bashPPExprScalarType(x.X); elem != nil {
			return &syntax.BashPPPointerType{Element: elem}
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
		// A declared function named as a value — `F(a)` converting `a` to a
		// named func type — is its handle, exactly as an argument slot binds
		// it. A generic function has no value until instantiated.
		if r.bashPPGoSource {
			if fn := r.bashPPFuncs[name]; fn != nil && len(fn.typeParams()) == 0 {
				return bashPPScalar{value: constant.MakeString(r.bashPPStoreFunc(fn).Str), runtime: true}, nil
			}
		}
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
	if cell.hasNonFiniteComplex {
		return bashPPNonFiniteComplexScalar(cell.nonFiniteComplex, cell.typeName)
	}
	if cell.hasNonFinite {
		return bashPPScalar{value: constant.MakeFloat64(0), typ: cell.typeName, runtime: true, nonFinite: cell.nonFinite, hasNonFinite: true}
	}
	if r.bashPPGoSource && cell.constant && cell.exactScalar != nil {
		value := bashPPScalar{value: cell.exactScalar}
		if named, ok := cell.declType.(*syntax.BashPPNamedType); ok {
			value.typ = named.Name.Value
		}
		return value
	}
	text := cell.vr.String()
	value := bashPPScalar{}
	if r.bashPPGoSource && cell.scalarKind != constant.String {
		typ := cell.typeName
		if typ == "" {
			typ = bashPPTypeText(cell.declType)
		}
		if underlying, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: typ}}).(*syntax.BashPPNamedType); ok &&
			underlying.Name != nil && (underlying.Name.Value == "complex64" || underlying.Name.Value == "complex128") {
			if special, ok := bashPPNonFiniteComplexText(text); ok {
				return bashPPNonFiniteComplexScalar(special, typ)
			}
		}
	}
	// A float struct field or element written from a NaN or infinity keeps
	// only the storage spelling; the cell flag above is set for variables.
	if r.bashPPGoSource && cell.scalarKind != constant.String {
		typ := cell.typeName
		if typ == "" {
			if named, ok := cell.declType.(*syntax.BashPPNamedType); ok && named.Name != nil {
				typ = named.Name.Value
			}
		}
		if cell.scalarKind == constant.Float || r.bashPPFloatTypeName(typ) {
			if special, ok := bashPPNonFiniteText(text); ok {
				return bashPPNonFiniteScalar(special, typ)
			}
		}
	}
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
					// Go result and declaration cells can carry their float
					// provenance only in declType. Decode the exact stored
					// rational before the untyped text fallback makes it a string.
					if r.bashPPGoSource {
						value.value = bashPPFloatText(text)
					}
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
	value.negativeZero = cell.negativeZero
	switch {
	case cell.typeName != "":
		value.typ = cell.typeName
	case cell.declType != nil:
		if named, ok := cell.declType.(*syntax.BashPPNamedType); ok {
			value.typ = named.Name.Value
		}
	case r.bashPPGoSource && value.runtime && value.value != nil && value.value.Kind() == constant.Float:
		// A Go variable is never untyped: `x := 0.1` declares a float64,
		// so arithmetic on it rounds at every step (0.1+0.2 is
		// 0.30000000000000004, not the exact 3/10 the constant folder
		// would keep) and it boxes into an interface as a float64.
		value.typ = "float64"
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

// bashPPNonFiniteText decodes the spelling under which a runtime IEEE value
// that go/constant cannot hold travels as text: "NaN", "+Inf" and "-Inf" as
// bashPPScalarStorageString writes them into a cell, and as the native bridge
// renders a float result such as math.NaN() or math.Inf(1). Any finite text,
// including one strconv would accept, reports false so the exact constant
// decoders keep owning it.
func bashPPNonFiniteText(text string) (float64, bool) {
	switch strings.ToLower(strings.TrimLeft(text, "+-")) {
	case "nan", "inf", "infinity":
	default:
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || (!math.IsInf(value, 0) && !math.IsNaN(value)) {
		return 0, false
	}
	return value, true
}

// bashPPNonFiniteScalar is the runtime scalar carrying a NaN or an infinity
// of the given float type; its constant carrier is the zero every consumer
// of hasNonFinite ignores.
func bashPPNonFiniteScalar(value float64, typ string) bashPPScalar {
	return bashPPScalar{value: constant.MakeFloat64(0), typ: typ, runtime: true, nonFinite: value, hasNonFinite: true}
}

// bashPPFloatTypeName reports whether the declared name (through any defined
// type) has a float32 or float64 underlying type.
func (r *Runner) bashPPFloatTypeName(name string) bool {
	if name == "" {
		return false
	}
	named, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: name}}).(*syntax.BashPPNamedType)
	return ok && named.Name != nil && (named.Name.Value == "float32" || named.Name.Value == "float64")
}

func bashPPScalarFloat64(value bashPPScalar) (float64, bool) {
	if value.hasNonFinite {
		return value.nonFinite, true
	}
	if value.value == nil || (value.value.Kind() != constant.Int && value.value.Kind() != constant.Float) {
		return 0, false
	}
	n, _ := constant.Float64Val(value.value)
	if n == 0 && value.negativeZero {
		n = math.Copysign(0, -1)
	}
	return n, true
}

// bashPPRuntimeFloatSpecial handles exactly the IEEE results which cannot be
// carried by go/constant. Ordinary finite arithmetic remains on the exact
// constant path below, and an untyped constant zero divisor still reports the
// language diagnostic there.
func (r *Runner) bashPPRuntimeFloatSpecial(op token.Token, left, right bashPPScalar, typ string) (bashPPScalar, bool) {
	if !r.bashPPGoSource || !(left.runtime || right.runtime) || (op != token.ADD && op != token.SUB && op != token.MUL && op != token.QUO) {
		return bashPPScalar{}, false
	}
	underlying, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: typ}}).(*syntax.BashPPNamedType)
	if !ok || (underlying.Name.Value != "float32" && underlying.Name.Value != "float64") {
		return bashPPScalar{}, false
	}
	lf, lok := bashPPScalarFloat64(left)
	rf, rok := bashPPScalarFloat64(right)
	if !lok || !rok {
		return bashPPScalar{}, false
	}
	var result float64
	switch op {
	case token.ADD:
		result = lf + rf
	case token.SUB:
		result = lf - rf
	case token.MUL:
		result = lf * rf
	case token.QUO:
		result = lf / rf
	}
	if underlying.Name.Value == "float32" {
		result = float64(float32(result))
	}
	if !math.IsInf(result, 0) && !math.IsNaN(result) {
		if left.hasNonFinite || right.hasNonFinite || left.negativeZero || right.negativeZero {
			// IEEE inputs can produce a finite result, notably -1 / +Inf.
			// Their dummy constant carriers must never reach constant folding.
			return bashPPScalar{value: constant.MakeFloat64(result), typ: typ, runtime: true, negativeZero: result == 0 && math.Signbit(result)}, true
		}
		return bashPPScalar{}, false
	}
	return bashPPScalar{value: constant.MakeFloat64(0), typ: typ, runtime: true, nonFinite: result, hasNonFinite: true}, true
}

func bashPPScalarStorageString(value bashPPScalar) string {
	if value.hasNonFiniteComplex {
		return strconv.FormatComplex(value.nonFiniteComplex, 'g', -1, 128)
	}
	if value.hasNonFinite {
		return strconv.FormatFloat(value.nonFinite, 'g', -1, 64)
	}
	return bashPPScalarString(value.value)
}

func (r *Runner) bashPPUnaryScalar(op token.Token, x bashPPScalar) (bashPPScalar, error) {
	if r.bashPPGoSource && x.hasNonFiniteComplex && (op == token.ADD || op == token.SUB) {
		value := x.nonFiniteComplex
		if op == token.SUB {
			value = -value
		}
		return bashPPNonFiniteComplexScalar(value, x.typ), nil
	}
	if r.bashPPGoSource && x.hasNonFinite && (op == token.ADD || op == token.SUB) {
		value := x.nonFinite
		if op == token.SUB {
			value = -value
		}
		return bashPPScalar{value: constant.MakeFloat64(0), typ: x.typ, runtime: true, nonFinite: value, hasNonFinite: true}, nil
	}
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
		result, err := r.bashPPTypedScalarResult(constant.UnaryOp(op, x.value, precision), x.typ, x.runtime)
		// go/constant intentionally has no signed zero. Go runtime floats do,
		// so retain that one bit only for an evaluated Go-source float.
		if r.bashPPGoSource && op == token.SUB && x.runtime && result.value.Kind() == constant.Float && constant.Sign(result.value) == 0 {
			result.negativeZero = !x.negativeZero
		}
		return result, err
	case token.NOT:
		if x.value.Kind() != constant.Bool {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: operator ! requires boolean operand")
		}
		return r.bashPPTypedScalarResult(constant.MakeBool(!constant.BoolVal(x.value)), x.typ, x.runtime)
	}
	return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: unsupported unary operator %s", op)
}

// bashPPCanonicalScalarType folds Go's predeclared aliases onto the types
// they name — byte is uint8 and rune is int32 — so two spellings of one type
// compare as identical. A script-declared type of the same name is its own.
func (r *Runner) bashPPCanonicalScalarType(name string) string {
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

func (r *Runner) bashPPBinaryScalar(op token.Token, left, right bashPPScalar) (bashPPScalar, error) {
	if r.bashPPGoSource {
		left.typ, right.typ = r.bashPPCanonicalScalarType(left.typ), r.bashPPCanonicalScalarType(right.typ)
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
	if value, ok := r.bashPPRuntimeFloatSpecial(op, left, right, resultType); ok {
		return value, nil
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
		if left.hasNonFiniteComplex || right.hasNonFiniteComplex {
			lf, lok := bashPPScalarComplex128(left)
			rf, rok := bashPPScalarComplex128(right)
			if lok && rok && (op == token.EQL || op == token.NEQ) {
				ok := lf == rf
				if op == token.NEQ {
					ok = !ok
				}
				return bashPPScalar{value: constant.MakeBool(ok)}, nil
			}
		}
		if left.hasNonFinite || right.hasNonFinite {
			lf, lok := bashPPScalarFloat64(left)
			rf, rok := bashPPScalarFloat64(right)
			if lok && rok {
				var ok bool
				switch op {
				case token.EQL:
					ok = lf == rf
				case token.NEQ:
					ok = lf != rf
				case token.LSS:
					ok = lf < rf
				case token.LEQ:
					ok = lf <= rf
				case token.GTR:
					ok = lf > rf
				case token.GEQ:
					ok = lf >= rf
				}
				return bashPPScalar{value: constant.MakeBool(ok)}, nil
			}
		}
		ok, err := bashPPCompareScalar(left.value, op, right.value)
		if err != nil {
			return bashPPScalar{}, err
		}
		return bashPPScalar{value: constant.MakeBool(ok)}, nil
	case token.QUO, token.REM:
		if right.value.Kind() == constant.Int && constant.Sign(right.value) == 0 {
			if r.bashPPGoSource {
				return bashPPScalar{}, r.bashPPRaiseRuntimeError(bashPPRuntimeErrorString, bashPPRuntimeErrorMessage+"integer divide by zero")
			}
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-DIVZERO: division by zero")
		}
		if right.value.Kind() == constant.Float {
			if f, _ := constant.Float64Val(right.value); f == 0 {
				// A typed runtime float is not a constant expression merely because
				// its current value is zero. Let the IEEE runtime rule produce Inf
				// or NaN; untyped constant divisions remain diagnostics.
				if r.bashPPGoSource && (left.runtime || right.runtime) {
					if value, ok := r.bashPPRuntimeFloatSpecial(op, left, right, resultType); ok {
						return value, nil
					}
				}
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
		// An untyped float constant whose value is an integer shifts as that
		// integer — `1e100 >> 1000` and `1 << 100 >> 100` in const.go. A
		// typed or runtime float operand, and a constant with a fraction,
		// keep the integer-operand diagnostic below.
		if r.bashPPGoSource && left.typ == "" && !left.runtime && left.value.Kind() == constant.Float {
			if integer := constant.ToInt(left.value); integer.Kind() == constant.Int {
				left.value = integer
			}
		}
		// constant.Uint64Val panics on a non-integer, and `1 << "a"` is now
		// reachable from source, so the kind is checked before the call rather
		// than recovered after it.
		shiftValue := constant.ToInt(right.value)
		// Go also permits an untyped complex count whose imaginary component
		// is zero (for example, `x << (1+0i)`).
		if shiftValue.Kind() != constant.Int && right.value.Kind() == constant.Complex && constant.Sign(constant.Imag(right.value)) == 0 {
			shiftValue = constant.ToInt(constant.Real(right.value))
		}
		if shiftValue.Kind() != constant.Int {
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-SHIFT: shift count must be an unsigned integer")
		}
		shift, ok := constant.Uint64Val(shiftValue)
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
		if bashPPPointerComparable(lv.meta) && bashPPPointerComparable(rv.meta) && bashPPTypeText(lv.meta.typ) == bashPPTypeText(rv.meta.typ) && r.bashPPZeroSizePointerEqual(lv.value, rv.value) {
			return op != token.NEQ, nil
		}
	}
	if equal, handled, err := r.goSourceInterfaceEqual(lv, rv); handled {
		if op == token.NEQ {
			equal = !equal
		}
		return equal, err
	}
	ok, err := r.bashPPCompareValues(lv.value, lv.meta, lv.nilLiteral, rv.value, rv.meta, rv.nilLiteral)
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
	case *syntax.BashPPBasicLit:
		value, err := r.bashPPEvalScalarExpr(x)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		return bashPPComparableValue{value: bashPPScalarAny(value.value)}, nil
	case *syntax.BashPPCall:
		if r.bashPPGoSource && bashPPRecoverExpr(x) && r.bashPPFuncs["recover"] == nil && (r.bashPPScope == nil || r.bashPPScope.lookup("recover") == nil) {
			iv, _ := r.bashPPRecoverInterfaceValue()
			return bashPPComparableValue{value: iv, meta: &bashPPCollectionMeta{kind: "interface", typ: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "any"}}, interfaceValue: iv}}, nil
		}
		if !r.bashPPGoSource {
			return bashPPComparableValue{}, fmt.Errorf("BASHPP-ECOMPARE-SCALAR: scalar")
		}
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		return bashPPComparableValue{value: value, meta: meta}, nil
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
				var kind string
				switch r.bashPPUnderlyingType(cell.declType).(type) {
				case *syntax.BashPPFuncType:
					kind = "func"
				case *syntax.BashPPChanType:
					kind = "channel"
				}
				if kind != "" {
					var value any = cell.vr.Str
					if cell.vr.Str == "" || cell.vr.Str == "nil" {
						value = nil
					}
					return bashPPComparableValue{value: value, meta: &bashPPCollectionMeta{kind: kind, typ: cell.declType, channel: cell.channel, channelOwner: cell.channelOwner}}, nil
				}
			}
		}
		if value, ok := r.goSourceFuncComparable(x.Name.Value); ok {
			return value, nil
		}
		return bashPPComparableValue{}, fmt.Errorf("BASHPP-ECOMPARE-SCALAR: scalar")
	case *syntax.BashPPAddressExpr, *syntax.BashPPNewExpr:
		ptr, err := r.bashPPPointerExprValue(expr)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		return bashPPComparableValue{value: ptr, meta: bashPPPointerMeta(&syntax.BashPPPointerType{Element: ptr.elem})}, nil
	case *syntax.BashPPConvertExpr:
		// `(*T)(p) == nil`: a pointer conversion compares as the pointer it
		// retypes; `v == any(x)` compares the interface value the conversion
		// boxes. Any other conversion is a scalar.
		ptr, target, converted, err := r.bashPPPointerConversion(x)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		if converted {
			return bashPPComparableValue{value: ptr, meta: bashPPPointerMeta(target)}, nil
		}
		if cell, handled, err := r.bashPPInterfaceConversion(x); handled {
			if err != nil {
				return bashPPComparableValue{}, err
			}
			return bashPPComparableValue{value: cell.interfaceValue, meta: &bashPPCollectionMeta{kind: "interface", typ: cell.declType}}, nil
		}
		if value, meta, handled, err := r.bashPPConvertToCollection(x); handled {
			if err != nil {
				return bashPPComparableValue{}, err
			}
			return bashPPComparableValue{value: value, meta: meta}, nil
		}
		if value, meta, handled, err := r.goSourceConvertedComposite(x); handled {
			if err != nil {
				return bashPPComparableValue{}, err
			}
			return bashPPComparableValue{value: value, meta: meta}, nil
		}
		if value, ok := r.goSourceTypedNilComparable(x); ok {
			return value, nil
		}
	case *syntax.BashPPDerefExpr:
		ptr, err := r.bashPPPointerExprValue(x.X)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		if ptr == nil {
			return bashPPComparableValue{}, errBashPPNilDereference
		}
		value, meta, _, err := ptr.read()
		if err != nil {
			return bashPPComparableValue{}, err
		}
		value, meta, err = r.bashPPSliceArrayPointerValue(ptr, value, meta)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		return bashPPComparableValue{value: value, meta: meta}, nil
	case *syntax.BashPPIndexExpr, *syntax.BashPPSliceExpr, *syntax.BashPPSelectorExpr:
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		if r.bashPPGoSource && meta == nil {
			meta = r.goSourceNilableScalarComparableMeta(expr)
		}
		return bashPPComparableValue{value: value, meta: meta}, nil
	case *syntax.BashPPCompositeLit:
		// `(T{1, 2}) == v`: a composite literal is the value it builds.
		if value, ok, err := r.goSourceCompositeComparable(x); ok {
			return value, err
		}
	case *syntax.BashPPBinaryExpr:
		// A comparison operand may itself be a scalar expression, as in
		// `x() == (y() == "abc")`. Evaluate it here, while walking the outer
		// comparison left-to-right. Returning the scalar-only sentinel after
		// the left call has run makes the caller retry the entire comparison,
		// evaluating that call twice and breaking Go's source-order rule.
		value, err := r.bashPPEvalScalarExpr(x)
		if err != nil {
			return bashPPComparableValue{}, err
		}
		if value.hasNonFinite {
			return bashPPComparableValue{value: value.nonFinite}, nil
		}
		if value.hasNonFiniteComplex {
			return bashPPComparableValue{value: value.nonFiniteComplex}, nil
		}
		return bashPPComparableValue{value: bashPPScalarAny(value.value)}, nil
	}
	return bashPPComparableValue{}, fmt.Errorf("BASHPP-ECOMPARE-SCALAR: scalar")
}

func bashPPCompareValues(left any, leftMeta *bashPPCollectionMeta, leftNilLiteral bool, right any, rightMeta *bashPPCollectionMeta, rightNilLiteral bool) (bool, error) {
	return bashPPCompareValuesWithRunner(nil, left, leftMeta, leftNilLiteral, right, rightMeta, rightNilLiteral)
}

func (r *Runner) bashPPCompareValues(left any, leftMeta *bashPPCollectionMeta, leftNilLiteral bool, right any, rightMeta *bashPPCollectionMeta, rightNilLiteral bool) (bool, error) {
	return bashPPCompareValuesWithRunner(r, left, leftMeta, leftNilLiteral, right, rightMeta, rightNilLiteral)
}

func bashPPCompareValuesWithRunner(r *Runner, left any, leftMeta *bashPPCollectionMeta, leftNilLiteral bool, right any, rightMeta *bashPPCollectionMeta, rightNilLiteral bool) (bool, error) {
	if leftNilLiteral || rightNilLiteral {
		if leftNilLiteral && rightNilLiteral {
			return false, fmt.Errorf("BASHPP-ECOMPARE-TYPE: nil cannot be compared with nil")
		}
		value, meta := left, leftMeta
		if leftNilLiteral {
			value, meta = right, rightMeta
		}
		if bashPPPointerComparable(meta) || bashPPNilComparable(meta) {
			return bashPPNilComparableValue(value) || bashPPNilComparableZero(value, meta), nil
		}
		if bashPPNilComparableZero(value, meta) {
			return true, nil
		}
		return false, fmt.Errorf("BASHPP-ECOMPARE-TYPE: value cannot be compared with nil")
	}
	// Go-source aggregates retain interface identity on each element's
	// metadata. Re-enter interface equality while walking an array or struct
	// so the dynamic value decides equality (or raises Go's runtime panic for
	// a non-comparable dynamic type). The nil runner keeps classic Bash++ on
	// its existing comparison path.
	if r != nil && r.bashPPGoSource {
		equal, handled, err := r.goSourceInterfaceEqual(
			bashPPComparableValue{value: left, meta: leftMeta},
			bashPPComparableValue{value: right, meta: rightMeta},
		)
		if handled {
			return equal, err
		}
	}
	if leftMeta == nil && rightMeta == nil {
		// The Go checker already established compatible operand types. A
		// contextual integer constant and a float returned by a call can
		// arrive here in different scalar storage types; compare their
		// numeric values without evaluating either expression again.
		if r != nil && r.bashPPGoSource {
			switch l := left.(type) {
			case float64:
				if n, ok := right.(int); ok {
					return l == float64(n), nil
				}
			case int:
				if n, ok := right.(float64); ok {
					return float64(l) == n, nil
				}
			}
		}
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
			ok, err := bashPPCompareValuesWithRunner(r, leftSeq[i], leftMeta.sequence[i], false, rightSeq[i], rightMeta.sequence[i], false)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, nil
			}
		}
		return true, nil
	case "struct":
		leftMap := bashPPStorageSnapshot(left.(map[string]any))
		rightMap := bashPPStorageSnapshot(right.(map[string]any))
		rightLayout := bashPPLayoutSnapshot(rightMeta.mapping)
		leftLayout := bashPPLayoutSnapshot(leftMeta.mapping)
		compareField := func(field string) (bool, error) {
			child := leftLayout[field]
			ok, err := bashPPCompareValuesWithRunner(r, leftMap[field], child, false, rightMap[field], rightLayout[field], false)
			if err != nil {
				return false, err
			}
			return ok, nil
		}
		if r != nil && r.bashPPGoSource {
			// Go compares struct fields in declaration order. That order is
			// observable when a later interface field contains a value whose
			// dynamic type is not comparable: an earlier mismatch must return
			// false before the panic-producing field is reached.
			if fields, _, ok := r.bashPPStructFields(leftMeta.typ); ok {
				for _, field := range bashPPFlatFields(fields) {
					if field.name == "_" {
						continue
					}
					ok, err := compareField(field.name)
					if err != nil || !ok {
						return ok, err
					}
				}
				return true, nil
			}
		}
		for field := range leftLayout {
			ok, err := compareField(field)
			if err != nil || !ok {
				return ok, err
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

func (r *Runner) goSourceNilableScalarComparableMeta(expr syntax.BashPPExpr) *bashPPCollectionMeta {
	typ := r.bashPPExprScalarType(expr)
	switch r.bashPPUnderlyingType(typ).(type) {
	case *syntax.BashPPFuncType:
		return &bashPPCollectionMeta{kind: "func", typ: typ}
	case *syntax.BashPPChanType:
		return &bashPPCollectionMeta{kind: "channel", typ: typ}
	}
	return nil
}

func bashPPNilComparableZero(value any, meta *bashPPCollectionMeta) bool {
	if value == nil {
		return true
	}
	// Collection zero values use typed nil payloads so expand.Object can keep
	// them distinct from an absent/invalid object. Recover their nilness here
	// without conflating an allocated empty slice or map with nil.
	if meta != nil {
		switch meta.kind {
		case "slice":
			if sequence, ok := value.([]any); ok {
				return sequence == nil
			}
		case "map":
			if mapping, ok := value.(map[string]any); ok {
				return mapping == nil
			}
		}
	}
	if native, ok := value.(*bashPPBridgeValue); ok && native != nil && native.Kind == "nil" {
		return true
	}
	if text, ok := value.(string); ok && text == "" && meta != nil && (meta.kind == "func" || meta.kind == "channel") {
		return true
	}
	return false
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
	if lp.target == rp.target && len(lp.path) == len(rp.path) {
		same := true
		for i := range lp.path {
			if lp.path[i] != rp.path[i] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	// Distinct spellings can address one storage slot when slice backing is
	// shared across a copy; see [bashPPPointer.slot].
	if lseq, li, ok := lp.slot(); ok {
		if rseq, ri, ok := rp.slot(); ok {
			return &lseq[li] == &rseq[ri]
		}
	}
	return false
}

// bashPPZeroSizePointerEqual matches Go's 1.27 address identity for distinct
// zero-size elements in one interpreter-owned aggregate.
func (r *Runner) bashPPZeroSizePointerEqual(left, right any) bool {
	lp, _ := left.(*bashPPPointer)
	rp, _ := right.(*bashPPPointer)
	if lp == nil || rp == nil || lp.target != rp.target {
		return false
	}
	leftShape, ok := r.bashPPGoShapeType(lp.elem, 0)
	if !ok || leftShape.Size() != 0 {
		return false
	}
	rightShape, ok := r.bashPPGoShapeType(rp.elem, 0)
	return ok && rightShape.Size() == 0
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
	// A constant complex with a zero imaginary part converts to the real
	// types — `T(0 + 0i)` with T ~float64 — exactly as Go's constant
	// conversions do; one with a nonzero imaginary part keeps failing below.
	if x.value != nil && x.value.Kind() == constant.Complex && typ != "complex64" && typ != "complex128" {
		if constant.Sign(constant.Imag(x.value)) == 0 {
			x.value = constant.ToFloat(constant.Real(x.value))
		}
	}
	switch typ {
	case "complex64", "complex128":
		return r.bashPPConvertComplex(typ, x)
	case "string":
		switch x.value.Kind() {
		case constant.String:
			return bashPPScalar{value: x.value, typ: typ, runtime: x.runtime}, nil
		case constant.Int:
			n, ok := constant.Int64Val(x.value)
			if ok {
				if r.bashPPGoSource {
					return bashPPScalar{value: constant.MakeString(string(bashPPStringRune(int64(n)))), typ: typ, runtime: x.runtime}, nil
				}
				return bashPPScalar{value: constant.MakeString(string(rune(n))), typ: typ, runtime: x.runtime}, nil
			}
			// GoSource scalar cells use shell text for storage. A uint64 can
			// therefore arrive here as an exact integer larger than int64 even
			// though Go permits converting every integer type to string via its
			// rune value. Keep Classic's old signed carrier boundary intact.
			if r.bashPPGoSource {
				if u, ok := constant.Uint64Val(x.value); ok {
					return bashPPScalar{value: constant.MakeString(string(bashPPStringRune(uint64(u)))), typ: typ, runtime: x.runtime}, nil
				}
			}
			return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert %s to string", x.value)
		}
	case "bool":
		if x.value.Kind() == constant.Bool {
			return bashPPScalar{value: x.value, typ: typ, runtime: x.runtime}, nil
		}
	case "float32", "float64":
		// A NaN or infinity converts between the float types as itself.
		if r.bashPPGoSource && x.hasNonFinite {
			return bashPPNonFiniteScalar(x.nonFinite, typ), nil
		}
		if x.value.Kind() == constant.Int || x.value.Kind() == constant.Float {
			// Only a constant overflows: Go rounds a runtime float64 that
			// exceeds float32's range to the infinity of its sign, as
			// `float32(v)` with v = -1.79769e+308 does in convinline.go.
			if typ == "float32" {
				value, _ := constant.Float32Val(x.value)
				if math.IsInf(float64(value), 0) {
					if r.bashPPGoSource && x.runtime {
						return bashPPNonFiniteScalar(float64(value), typ), nil
					}
					return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: constant %s overflows %s", x.value, typ)
				}
			} else {
				value, _ := constant.Float64Val(x.value)
				if math.IsInf(value, 0) {
					if r.bashPPGoSource && x.runtime {
						return bashPPNonFiniteScalar(value, typ), nil
					}
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
			return bashPPScalar{value: value, typ: typ, runtime: x.runtime, negativeZero: x.negativeZero && constant.Sign(value) == 0}, nil
		}
	default:
		if converted, ok, err := r.bashPPConvertGoSourceStringToUint64(typ, x); ok {
			return converted, err
		}
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

func (r *Runner) bashPPConvertGoSourceStringToUint64(typ string, x bashPPScalar) (bashPPScalar, bool, error) {
	if !r.bashPPGoSource || !x.runtime || typ != "uint64" || x.value.Kind() != constant.String {
		return bashPPScalar{}, false, nil
	}
	source, ok := r.bashPPGoSourceStringCarrierType(x.typ)
	if !ok || source == "string" {
		return bashPPScalar{}, false, nil
	}
	switch source {
	case "float32", "float64":
		value := bashPPFloatText(constant.StringVal(x.value))
		if value.Kind() != constant.Float {
			return bashPPScalar{}, true, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert String to %s", typ)
		}
		converted, err := r.bashPPConvertScalar(typ, bashPPScalar{value: value, typ: source, runtime: true})
		return converted, true, err
	case "byte", "int", "int8", "int16", "int32", "int64", "rune",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		value := constant.MakeFromLiteral(constant.StringVal(x.value), token.INT, 0)
		if value.Kind() != constant.Int || !bashPPIntegerRepresentable(source, value) {
			return bashPPScalar{}, true, fmt.Errorf("BASHPP-EEXPR-CONVERT: cannot convert String to %s", typ)
		}
		converted, err := r.bashPPConvertScalar(typ, bashPPScalar{value: value, typ: source, runtime: true})
		return converted, true, err
	}
	return bashPPScalar{}, false, nil
}

func (r *Runner) bashPPGoSourceStringCarrierType(typ string) (string, bool) {
	if typ == "" {
		return "", false
	}
	if bashPPBuiltinType(typ) {
		return typ, true
	}
	named := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: typ}}
	shape, ok := r.bashPPUnderlyingType(named).(*syntax.BashPPNamedType)
	if !ok || shape == named || shape.Name == nil || !bashPPBuiltinType(shape.Name.Value) {
		return "", false
	}
	return shape.Name.Value, true
}

// bashPPStringRune implements Go's integer-to-string conversion. It is not an
// ordinary conversion to rune: an integer outside the Unicode scalar range,
// including a uint64 that cannot fit in int64, becomes U+FFFD rather than
// wrapping through the target integer width.
func bashPPStringRune[T int64 | uint64](value T) rune {
	const replacement = '\uFFFD'
	const maxRune = 0x10FFFF
	const surrogateFirst = 0xD800
	const surrogateLast = 0xDFFF
	if value < 0 || value > maxRune || value >= surrogateFirst && value <= surrogateLast {
		return replacement
	}
	return rune(value)
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
		if r.goSourceNilFuncCallee(call) {
			return bashPPScalar{}, r.goSourceRuntimeFault(errBashPPNilDereference)
		}
		if r.goSourceCalleeFaulted() {
			return bashPPScalar{}, errBashPPScalarInterrupted
		}
		name := "computed function"
		if len(call.Fun) > 0 {
			name = call.Fun[0].Value
		}
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-UNDEFINED: undefined callable %s", name)
	}
	// A native callable may name an imported result type which has no local
	// signature tree. Its returned typed cells below are the result authority.
	if fn.native == nil && bashppResultCount(fn.results()) != 1 {
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
	if r.bashPPPanicHalts() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != failure || len(values) != 1 {
		return bashPPScalar{}, errBashPPScalarInterrupted
	}
	if len(r.bashPPResultCells) != 1 {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CALL: scalar result metadata missing")
	}
	result := r.bashPPScalarFromCell(r.bashPPResultCells[0])
	if result.kind() == constant.Unknown {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-OPERAND: call result is not a scalar")
	}
	return result, nil
}

// A call may have already reported its failure, or may be unwinding through
// panic/exit. Scalar statement consumers must retain that state unchanged.
var errBashPPScalarInterrupted = errors.New("scalar call interrupted")
