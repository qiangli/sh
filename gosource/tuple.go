package gosource

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	s "mvdan.cc/sh/v3/syntax"
)

// tupleValueDecls evaluates a checked multi-result initializer exactly once.
// Temporaries use the collision-free package prefix and bind before the real
// declarations, preserving the Go rule that a local variable's scope begins
// at the end of its ValueSpec. Each actual declaration keeps its checked type,
// including an explicit type for the otherwise untyped comma-ok boolean.
func (c *converter) tupleValueDecls(g *ast.GenDecl, v *ast.ValueSpec) []*s.Stmt {
	temps := make([]ast.Expr, len(v.Names))
	for i, name := range v.Names {
		temps[i] = &ast.Ident{NamePos: name.Pos(), Name: fmt.Sprintf("%stuple_%d_%d", c.prefix, v.Pos(), i)}
	}
	var out []*s.Stmt
	rhs := []ast.Expr{ast.Unparen(v.Values[0])}
	// Assertions consume an interface cell. Materialize a computed operand
	// once before the assertion rather than evaluating its call as a scalar.
	if assertion, ok := rhs[0].(*ast.TypeAssertExpr); ok {
		copy := *assertion
		copy.X = ast.Unparen(assertion.X)
		if _, simple := copy.X.(*ast.Ident); !simple {
			operand := &ast.Ident{NamePos: assertion.X.Pos(), Name: fmt.Sprintf("%stuple_operand_%d", c.prefix, v.Pos())}
			out = append(out, c.statements(&ast.AssignStmt{Lhs: []ast.Expr{operand}, TokPos: v.Pos(), Tok: token.DEFINE, Rhs: []ast.Expr{copy.X}})...)
			copy.X = operand
		}
		rhs = []ast.Expr{&copy}
	}
	out = append(out, c.statements(&ast.AssignStmt{Lhs: temps, TokPos: v.Pos(), Tok: token.DEFINE, Rhs: rhs})...)
	zero := *v
	zero.Values = nil
	for i, name := range v.Names {
		if name.Name == "_" {
			// Consume the temporary without declaring a blank binding. This
			// also keeps retained generic Go bodies valid (no unused temps).
			out = append(out, c.statements(&ast.AssignStmt{Lhs: []ast.Expr{name}, TokPos: name.Pos(), Tok: token.ASSIGN, Rhs: []ast.Expr{temps[i]}})...)
			continue
		}
		decl := c.valueDecl(g, &zero, name, i)
		decl.Init = []*s.Word{c.word(temps[i])}
		decl.InitExpr = c.expr(temps[i])
		if i == 1 && c.info.Types[ast.Unparen(v.Values[0])].HasOk() {
			// Restore the checked boolean identity even on runtime paths
			// whose comma-ok temporary stores only its textual value.
			name := c.lit(v.Pos(), "bool")
			var typ s.BashPPTypeExpr = &s.BashPPNamedType{Name: name}
			if v.Type != nil && tupleBooleanType(c.info.TypeOf(v.Type)) {
				name = c.lit(v.Type.Pos(), c.text(v.Type))
				typ = c.typ(v.Type)
			}
			decl.InitExpr = &s.BashPPConvertExpr{ConvType: name, ConvTypeExpr: typ, Lparen: c.pos(v.Pos()), Rparen: c.pos(v.End()), X: decl.InitExpr}
		}
		out = append(out, c.stmt(decl))
	}
	return out
}

func tupleBooleanType(typ types.Type) bool {
	if _, ok := typ.(*types.TypeParam); ok {
		return true
	}
	basic, ok := typ.Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Bool
}
