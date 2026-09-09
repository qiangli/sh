package gosource

import (
	"go/constant"
	"go/token"
	"strings"

	s "mvdan.cc/sh/v3/syntax"
)

// Retain exact real and imaginary components, including rational constants.
// No float conversion occurs until the interpreter assigns a runtime type.
func (c *converter) complexConstantExpr(pos token.Pos, value constant.Value) s.BashPPExpr {
	component := func(v constant.Value) s.BashPPExpr {
		parts := strings.Split(v.ExactString(), "/")
		lit := func(text string) *s.BashPPBasicLit {
			return &s.BashPPBasicLit{Kind: "FLOAT", Value: c.lit(pos, text+".0")}
		}
		if len(parts) == 2 {
			return &s.BashPPBinaryExpr{X: lit(parts[0]), Op: c.lit(pos, "/"), Y: lit(parts[1])}
		}
		return lit(parts[0])
	}
	imag := &s.BashPPBinaryExpr{X: component(constant.Imag(value)), Op: c.lit(pos, "*"), Y: &s.BashPPBasicLit{Kind: "IMAG", Value: c.lit(pos, "1i")}}
	return &s.BashPPBinaryExpr{X: component(constant.Real(value)), Op: c.lit(pos, "+"), Y: imag}
}
