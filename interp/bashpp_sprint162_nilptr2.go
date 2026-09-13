package interp

import (
	"go/constant"
	"go/token"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Sprint 162, lane interp-nilptr-2: typed scalar storage kind.
//
// A typed float declaration (`var f float32 = 3.14159`) stores the converted
// constant's exact text, which for a rational value is `n/d`. The cell must
// read back as the float it declares, whatever the shell text looks like, so
// that unary and binary operators see a Float operand rather than a String.

// bashPPFloatStorage reports whether the cell holds a floating-point scalar:
// either its recorded kind says so, or its declared type's underlying type is
// float32 / float64.
func (r *Runner) bashPPFloatStorage(cell *bashPPCell) bool {
	if cell.scalarKind == constant.Float {
		return true
	}
	if cell.declType == nil {
		return false
	}
	named, ok := r.bashPPUnderlyingType(cell.declType).(*syntax.BashPPNamedType)
	return ok && named.Name != nil && (named.Name.Value == "float32" || named.Name.Value == "float64")
}

// bashPPFloatText reads a float cell's text: a Go float literal, or the exact
// `n/d` rendering of a rational value. The result is Unknown for other text.
func bashPPFloatText(text string) constant.Value {
	if v := constant.MakeFromLiteral(text, token.FLOAT, 0); v.Kind() != constant.Unknown {
		return v
	}
	if exact := bashPPExactFloatText(text); exact != nil {
		return exact
	}
	return constant.MakeUnknown()
}

// bashPPExactFloatText parses the exact `numerator/denominator` rendering that
// bashPPScalarString gives a rational float value. It returns nil for any
// other text.
func bashPPExactFloatText(text string) constant.Value {
	parts := strings.Split(text, "/")
	if len(parts) != 2 {
		return nil
	}
	numerator := constant.MakeFromLiteral(parts[0], token.FLOAT, 0)
	denominator := constant.MakeFromLiteral(parts[1], token.FLOAT, 0)
	if numerator.Kind() == constant.Unknown || denominator.Kind() == constant.Unknown || constant.Sign(denominator) == 0 {
		return nil
	}
	return constant.BinaryOp(numerator, token.QUO, denominator)
}

// bashPPBuiltinScalarKind is the constant kind a builtin scalar type's cells
// store; Unknown for a type that is not a builtin scalar.
func bashPPBuiltinScalarKind(name string) constant.Kind {
	switch {
	case name == "string":
		return constant.String
	case name == "bool":
		return constant.Bool
	case name == "float32" || name == "float64":
		return constant.Float
	case name == "complex64" || name == "complex128":
		return constant.Complex
	case bashPPIntegerType(name):
		return constant.Int
	}
	return constant.Unknown
}
