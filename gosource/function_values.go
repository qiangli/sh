package gosource

import (
	"go/ast"
	"go/parser"
	"go/types"

	"mvdan.cc/sh/v3/syntax"
)

func (c *converter) functionValueType(expr ast.Expr) *syntax.BashPPFuncType {
	typ := c.info.TypeOf(expr)
	if typ == nil {
		return nil
	}
	typeSignature, ok := typ.Underlying().(*types.Signature)
	if !ok {
		return nil
	}
	// A named function type (notably iter.Seq[T]) prints as its name. The
	// interpreter needs its callable shape, so retain the underlying signature.
	text := c.typeString(typeSignature)
	parsed, err := parser.ParseExpr(text)
	if err != nil {
		c.fail(expr, "function value type")
		return nil
	}
	previous := c.syntheticPos
	c.syntheticPos = expr.Pos()
	defer func() { c.syntheticPos = previous }()
	signature, _ := c.typ(parsed).(*syntax.BashPPFuncType)
	return signature
}
