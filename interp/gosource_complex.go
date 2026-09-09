package interp

// GoSource complex values use the existing exact constant carrier. Runtime
// results round each component to the declared Go width before storage.
import (
	"fmt"
	"go/constant"
	"go/token"
	"math"
	"strconv"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func bashPPComplexConstant(z complex128) constant.Value {
	return constant.BinaryOp(constant.MakeFloat64(real(z)), token.ADD, constant.MakeImag(constant.MakeFloat64(imag(z))))
}

func (r *Runner) bashPPComplexShortDecl(d *syntax.BashPPShortDecl) bool {
	if d.Call == nil || len(d.Lhs) != 1 {
		return false
	}
	v, handled, err := r.bashPPComplexBuiltin(d.Call)
	if !handled {
		return false
	}
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return true
	}
	if v.typ == "" {
		switch v.value.Kind() {
		case constant.Complex:
			v.typ = "complex128"
		case constant.Float:
			v.typ = "float64"
		}
	}
	r.bashPPDeclareName(d.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(v.value)})
	cell := r.bashPPScope.lookup(d.Lhs[0].Value)
	cell.scalarKind = v.value.Kind()
	cell.typeName = v.typ
	return true
}
func bashPPComplexNumber(v constant.Value) complex128 {
	re, _ := constant.Float64Val(constant.Real(v))
	im, _ := constant.Float64Val(constant.Imag(v))
	return complex(re, im)
}

// Go's runtime operations round intermediate floating components too. Using
// constant.BinaryOp for runtime multiplication would incorrectly keep exact
// products until the final addition and can change cancellation results.
func (r *Runner) bashPPComplexRuntimeOp(op token.Token, left, right bashPPScalar, typ string) (bashPPScalar, bool, error) {
	if !r.bashPPGoSource || !(left.runtime || right.runtime) || (left.value.Kind() != constant.Complex && right.value.Kind() != constant.Complex) {
		return bashPPScalar{}, false, nil
	}
	if op != token.ADD && op != token.SUB && op != token.MUL && op != token.QUO {
		return bashPPScalar{}, false, nil
	}
	base := typ
	if named, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: typ}}).(*syntax.BashPPNamedType); ok {
		base = named.Name.Value
	}
	a, b := bashPPComplexNumber(left.value), bashPPComplexNumber(right.value)
	var z complex128
	if base == "complex64" {
		a, b := complex64(a), complex64(b)
		switch op {
		case token.ADD:
			z = complex128(a + b)
		case token.SUB:
			z = complex128(a - b)
		case token.MUL:
			z = complex128(a * b)
		case token.QUO:
			z = complex128(a / b)
		}
	} else {
		switch op {
		case token.ADD:
			z = a + b
		case token.SUB:
			z = a - b
		case token.MUL:
			z = a * b
		case token.QUO:
			z = a / b
		}
	}
	if math.IsInf(real(z), 0) || math.IsInf(imag(z), 0) || math.IsNaN(real(z)) || math.IsNaN(imag(z)) {
		return bashPPScalar{}, true, fmt.Errorf("BASHPP-EEXPR-CONVERT: non-finite complex runtime result is not supported by scalar carrier")
	}
	return bashPPScalar{value: bashPPComplexConstant(z), typ: typ, runtime: true}, true, nil
}
func bashPPParseComplex(text string) constant.Value {
	z, err := strconv.ParseComplex(text, 128)
	if err != nil {
		return constant.MakeUnknown()
	}
	return bashPPComplexConstant(z)
}
func (r *Runner) bashPPConvertComplex(typ string, x bashPPScalar) (bashPPScalar, error) {
	if !r.bashPPGoSource {
		return bashPPScalar{}, fmt.Errorf("BASHPP-ECOMPLEX-UNSUPPORTED: complex values require Go source")
	}
	if x.value.Kind() != constant.Int && x.value.Kind() != constant.Float && x.value.Kind() != constant.Complex {
		return bashPPScalar{}, fmt.Errorf("cannot convert %s to %s", x.value.Kind(), typ)
	}
	z := bashPPComplexNumber(x.value)
	if typ == "complex64" {
		re, _ := constant.Float32Val(constant.Real(x.value))
		im, _ := constant.Float32Val(constant.Imag(x.value))
		z = complex128(complex(re, im))
	}
	if math.IsInf(real(z), 0) || math.IsInf(imag(z), 0) || math.IsNaN(real(z)) || math.IsNaN(imag(z)) {
		return bashPPScalar{}, fmt.Errorf("BASHPP-EEXPR-CONVERT: non-finite %s is not supported by scalar carrier", typ)
	}
	return bashPPScalar{value: bashPPComplexConstant(z), typ: typ, runtime: x.runtime}, nil
}
func (r *Runner) bashPPComplexBuiltin(call *syntax.BashPPCall) (bashPPScalar, bool, error) {
	if !r.bashPPGoSource || len(call.Fun) != 1 {
		return bashPPScalar{}, false, nil
	}
	name := call.Fun[0].Value
	if name != "complex" && name != "real" && name != "imag" {
		return bashPPScalar{}, false, nil
	}
	if _, ok := r.bashPPLookupFunc(call); ok {
		return bashPPScalar{}, false, nil
	}
	count := 1
	if name == "complex" {
		count = 2
	}
	if len(call.ArgExprs) != count {
		return bashPPScalar{}, true, fmt.Errorf("%s requires %d arguments", name, count)
	}
	args := make([]bashPPScalar, count)
	for i, expr := range call.ArgExprs {
		v, err := r.bashPPEvalScalarExpr(expr)
		if err != nil {
			return bashPPScalar{}, true, err
		}
		args[i] = v
	}
	if name == "complex" {
		for _, a := range args {
			if a.value.Kind() != constant.Int && a.value.Kind() != constant.Float {
				return bashPPScalar{}, true, fmt.Errorf("complex requires floating-point arguments")
			}
		}
		typ := ""
		runtime := args[0].runtime || args[1].runtime
		for _, a := range args {
			if a.typ != "" {
				underlying, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: a.typ}}).(*syntax.BashPPNamedType)
				if !ok || (underlying.Name.Value != "float32" && underlying.Name.Value != "float64") {
					return bashPPScalar{}, true, fmt.Errorf("complex requires floating-point arguments")
				}
				if underlying.Name.Value == "float32" {
					typ = "complex64"
				} else {
					typ = "complex128"
				}
			}
		}
		v := constant.BinaryOp(args[0].value, token.ADD, constant.MakeImag(args[1].value))
		result, err := r.bashPPTypedScalarResult(v, typ, runtime)
		return result, true, err
	}
	a := args[0]
	if a.value.Kind() != constant.Complex && a.value.Kind() != constant.Int && a.value.Kind() != constant.Float {
		return bashPPScalar{}, true, fmt.Errorf("%s requires complex argument", name)
	}
	typ := ""
	if a.typ != "" {
		underlying, ok := r.bashPPUnderlyingType(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: a.typ}}).(*syntax.BashPPNamedType)
		if !ok {
			return bashPPScalar{}, true, fmt.Errorf("%s requires complex argument", name)
		}
		switch underlying.Name.Value {
		case "complex64":
			typ = "float32"
		case "complex128":
			typ = "float64"
		default:
			return bashPPScalar{}, true, fmt.Errorf("%s requires complex argument", name)
		}
	}
	v := constant.Real(a.value)
	if name == "imag" {
		v = constant.Imag(a.value)
	}
	return bashPPScalar{value: constant.ToFloat(v), typ: typ, runtime: a.runtime}, true, nil
}
