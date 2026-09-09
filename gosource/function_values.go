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
	if _, ok := typ.Underlying().(*types.Signature); !ok {
		return nil
	}
	text := types.TypeString(typ, func(p *types.Package) string {
		if p.Path() == c.packagePath {
			return ""
		}
		if alias := c.importAliases[p.Path()]; alias != "" {
			return alias
		}
		return p.Name()
	})
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
