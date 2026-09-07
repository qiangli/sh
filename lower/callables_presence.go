package lower

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (e *emitter) selectorProjection(n *syntax.BashPPSelectorExpr) projection {
	if n.Sel == nil {
		return scalarProjection()
	}
	base := e.projectionExpr(n.X)
	return e.fieldProjection(strings.TrimPrefix(base.sourceType, "*"), n.Sel.Value, map[string]bool{})
}
func (e *emitter) fieldProjection(typ, name string, seen map[string]bool) projection {
	if seen[typ] {
		return scalarProjection()
	}
	seen[typ] = true
	declaration := e.declaredTypes[typ]
	if declaration == nil {
		return scalarProjection()
	}
	fields := declaration.StructFields
	if structure, ok := declaration.DeclTypeExpr.(*syntax.BashPPStructType); ok {
		fields = structure.Fields
	}
	for _, field := range fields {
		for _, fieldName := range field.Names {
			if fieldName.Value == name {
				if text, err := e.fieldType(field); err == nil {
					return e.projectionType(text, nil)
				}
			}
		}
	}
	for _, field := range fields {
		if field.Embedded {
			if text, err := e.fieldType(field); err == nil {
				if nested := e.fieldProjection(strings.TrimPrefix(text, "*"), name, seen); nested.sourceType != "" {
					return nested
				}
			}
		}
	}
	if len(fields) == 0 {
		if declaration.DeclTypeExpr != nil {
			if text, err := e.typeExpr(declaration.DeclTypeExpr); err == nil && text != typ {
				return e.fieldProjection(text, name, seen)
			}
		}
		if declaration.DeclType != nil && declaration.DeclType.Value != typ {
			return e.fieldProjection(declaration.DeclType.Value, name, seen)
		}
	}
	return scalarProjection()
}
func (e *emitter) checkedSelector(n *syntax.BashPPSelectorExpr) (string, error) {
	base, err := e.expr(n.X)
	if err != nil {
		return "", err
	}
	if n.Sel == nil {
		return "", e.fail(n, CodeExpr, "missing selector name")
	}
	if info := e.projectionExpr(n.X); info.kind == projectPointer {
		base = e.checkedDeref(n, base, "")
	}
	return "(" + base + ")." + n.Sel.Value, nil
}

func (e *emitter) checkedShortDeclaration(n *syntax.BashPPShortDecl, rhs string) (string, bool, error) {
	if len(n.Lhs) != 1 || n.Expr == nil || n.Lhs[0].Value == "_" || e.scopes[len(e.scopes)-1][n.Lhs[0].Value] {
		return "", false, nil
	}
	risky := false
	syntax.Walk(n.Expr, func(node syntax.Node) bool {
		switch x := node.(type) {
		case *syntax.BashPPDerefExpr:
			risky = true
		case *syntax.BashPPSelectorExpr:
			risky = risky || e.projectionExpr(x.X).kind == projectPointer
		}
		return true
	})
	if !risky {
		return "", false, nil
	}
	info := e.projectionExpr(n.Expr)
	if info.sourceType == "" {
		return "", false, nil
	}
	name := n.Lhs[0].Value
	failure := e.prefix + "bindingError" + strings.ReplaceAll(n.Pos().String(), ":", "_")
	info.present = failure + " == nil"
	e.bind(name)
	e.projections.projectionBind(name, info)
	text := name + ", " + failure + " := " + e.prefix + "rt.TryValue(func() " + info.sourceType + " { return " + rhs + " })\nif " + failure + " != nil {" + e.operationFailure(failure) + "}" + e.unused([]string{name})
	return text, true, nil
}
