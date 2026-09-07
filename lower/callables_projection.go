package lower

import (
	"bytes"
	"go/ast"
	"go/constant"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"mvdan.cc/sh/v3/syntax"
	"strconv"
	"strings"
)

func (e *emitter) projectionType(text string, seen map[string]bool) (out projection) {
	defer func() { out.sourceType = text }()
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[text] {
		return scalarProjection()
	}
	seen[text] = true
	name := text
	if bracket := strings.IndexByte(text, '['); bracket > 0 && !strings.HasPrefix(text, "map[") {
		name = text[:bracket]
	}
	if decl := e.declaredTypes[name]; decl != nil {
		if decl.DeclTypeExpr != nil {
			typ, err := e.typeExpr(decl.DeclTypeExpr)
			if err == nil {
				return e.projectionType(typ, seen)
			}
		}
		if decl.DeclType != nil {
			return e.projectionType(decl.DeclType.Value, seen)
		}
	}
	switch {
	case strings.HasPrefix(text, "*"):
		p := pointerProjection()
		element := e.projectionType(strings.TrimPrefix(text, "*"), seen)
		p.element = &element
		return p
	case strings.HasPrefix(text, "[") || strings.HasPrefix(text, "map[") || strings.HasPrefix(text, "struct"):
		p := nativeAggregateProjection()
		if expr, err := parser.ParseExpr(text); err == nil {
			var node ast.Expr
			switch n := expr.(type) {
			case *ast.ArrayType:
				node = n.Elt
			case *ast.MapType:
				node = n.Value
			}
			if node != nil {
				var out bytes.Buffer
				_ = format.Node(&out, token.NewFileSet(), node)
				element := e.projectionType(out.String(), seen)
				p.element = &element
			}
		}
		return p
	case text == "any" || text == "error" || strings.HasPrefix(text, "interface"):
		return interfaceProjection()
	case text == "float64" || text == "float32":
		return projection{kind: projectFloat}
	}
	return scalarProjection()
}
func (e *emitter) projectionExpr(x syntax.BashPPExpr) projection {
	switch n := x.(type) {
	case *syntax.BashPPBasicLit:
		if n.Kind == "FLOAT" {
			p, err := projectionFromLiteral(n)
			if err == nil {
				return p
			}
		}
	case *syntax.BashPPIdent:
		if p, ok := e.projections.projectionLookup(n.Name.Value); ok {
			return p
		}
	case *syntax.BashPPSelectorExpr:
		return e.selectorProjection(n)
	case *syntax.BashPPCompositeLit:
		if n.LitType != nil {
			typ, err := e.typeExpr(n.LitType)
			if err == nil {
				return e.projectionType(typ, nil)
			}
		}
		return nativeAggregateProjection()
	case *syntax.BashPPAddressExpr:
		p := pointerProjection()
		element := e.projectionExpr(n.X)
		p.element = &element
		return p
	case *syntax.BashPPNewExpr:
		p := pointerProjection()
		typ, err := e.typeExpr(n.AllocType)
		if err == nil {
			element := e.projectionType(typ, nil)
			p.element = &element
		}
		return p
	case *syntax.BashPPDerefExpr:
		p := e.projectionExpr(n.X)
		if p.element != nil {
			return *p.element
		}
	case *syntax.BashPPIndexExpr:
		p := e.projectionExpr(n.X)
		if p.element != nil {
			return *p.element
		}
	case *syntax.BashPPTypeAssertExpr:
		if n.Assert != nil {
			typ, err := e.typeExpr(n.Assert)
			if err == nil {
				return e.projectionType(typ, nil)
			}
		}

	case *syntax.BashPPParenExpr:
		return e.projectionExpr(n.X)
	}
	return scalarProjection()
}
func (e *emitter) projectionWord(w *syntax.Word) projection {
	if w == nil {
		return scalarProjection()
	}
	if name := w.Lit(); name != "" {
		if p, ok := e.projections.projectionLookup(name); ok {
			return p
		}
		var scan scanner.Scanner
		scan.Init(token.NewFileSet().AddFile("", -1, len(name)), []byte(name), nil, 0)
		_, kind, _ := scan.Scan()
		if kind != token.FLOAT {
			if kind == token.INT {
				return e.projectionType("int", nil)
			}
			if kind == token.STRING {
				return e.projectionType("string", nil)
			}
			if name == "true" || name == "false" {
				return e.projectionType("bool", nil)
			}
			return scalarProjection()
		}
		value := constant.MakeFromLiteral(name, token.FLOAT, 0)
		if value.Kind() == constant.Float {
			return projection{kind: projectFloat, text: value.ExactString(), hasText: true}
		}
	}
	return scalarProjection()
}
func (e *emitter) declarationProjection(n *syntax.BashPPDecl) projection {
	var p projection
	if n.DeclTypeExpr != nil {
		typ, err := e.typeExpr(n.DeclTypeExpr)
		if err == nil {
			p = e.projectionType(typ, nil)
		}
	} else if n.DeclType != nil {
		p = e.projectionType(n.DeclType.Value, nil)
	}
	if p.kind == projectFloat {
		p.runtimeFloat = true
		return p
	}
	if n.DeclType == nil && n.DeclTypeExpr == nil {
		if n.InitExpr != nil {
			return e.projectionExpr(n.InitExpr)
		}
		if len(n.Init) == 1 {
			return e.projectionWord(n.Init[0])
		}
	}
	return p
}
func (e *emitter) callProjection(c *syntax.BashPPCall, index int) projection {
	if c == nil {
		return scalarProjection()
	}
	if len(c.Fun) > 1 && e.imports[c.Fun[0].Value] != "" {
		return objectProjection()
	}
	if len(c.Fun) == 1 {
		name := c.Fun[0].Value
		if name == "float32" || name == "float64" {
			return projection{kind: projectFloat}
		}
		if name == "make" && c.ArgType != nil {
			return nativeAggregateProjection()
		}
		if f := e.functionDecls[name]; f != nil {
			i := 0
			for _, field := range f.Results {
				count := len(field.Names)
				if count == 0 {
					count = 1
				}
				if index >= i && index < i+count {
					typ, err := e.fieldType(field)
					if err == nil {
						return e.projectionType(typ, nil)
					}
				}
				i += count
			}
		}
	}
	return scalarProjection()
}
func (e *emitter) projectBinding(n syntax.Node, name, expression string) (text string, err error) {
	if info, ok := e.projections.projectionLookup(name); ok && info.emptyWhen != "" {
		defer func() {
			if err == nil {
				text = e.prefix + "rt.BindingValue(!(" + info.emptyWhen + ")," + text + ",\"\")"
			}
		}()
	}

	if info, ok := e.projections.projectionLookup(name); ok && info.present != "" {
		defer func() {
			if err == nil {
				text = e.prefix + "rt.BindingValue(" + info.present + ", " + text + ", \"\")"
			}
		}()
	}
	p, ok := e.projections.projectionLookup(name)
	if !ok {
		return expression, nil
	}
	if p.runtimeFloat {
		e.bridge = true
		return e.prefix + "rt.TypedFloatProjection(" + expression + ")", nil
	}
	if p.receiver && p.kind == projectPointer {
		e.bridge = true
		return e.prefix + "rt.ReceiverProjection(" + expression + ")", nil
	}
	if e.writtenNames[name] && (p.kind == projectFloat || p.kind == projectObject && !p.nativeAggregate) {
		return "", e.fail(n, CodeUnsupported, "mutable exact/object projection requires runtime provenance storage")
	}
	if p.kind == projectScalar && !p.hasText {
		return expression, nil
	}
	value, err := e.projections.projectValue(name, expression)
	if err != nil {
		return "", e.fail(n, CodeExpr, err.Error())
	}
	if !p.hasText {
		e.bridge = true
		value = strings.ReplaceAll(value, "shellrt.", e.prefix+"rt.")
	}
	return value, nil
}
func (e *emitter) projectionArgument(w *syntax.Word, expression string) (text string, err error) {
	if info, ok := e.projections.projectionLookup(w.Lit()); ok && info.emptyWhen != "" {
		defer func() {
			if err == nil {
				text = e.prefix + "rt.BindingValue(!(" + info.emptyWhen + ")," + text + ",\"\")"
			}
		}()
	}

	if info, ok := e.projections.projectionLookup(w.Lit()); ok && info.present != "" {
		defer func() {
			if err == nil {
				text = e.prefix + "rt.BindingValue(" + info.present + ", " + text + ", " + strconv.Quote(w.Lit()) + ")"
			}
		}()
	}
	if name := w.Lit(); name != "" {
		if e.bound(name) {
			p, ok := e.projections.projectionLookup(name)
			if !ok {
				return expression, nil
			}
			if p.kind == projectFloat {
				return e.projectBinding(w, name, expression)
			}
			if p.nativeAggregate {
				e.bridge = true
				return e.prefix + "rt.NativePrintValue(" + expression + ")", nil
			}
			return expression, nil
		}
		p := e.projectionWord(w)
		if p.hasText {
			return strconv.Quote(p.text), nil
		}
	}
	return expression, nil
}

func nativeAggregateProjection() projection {
	p := objectProjection()
	p.nativeAggregate = true
	return p
}

// A whole-unit write audit prevents constant provenance crossing unknown calls
// or control-flow merges. Mutable exact/object provenance is a later runtime
// storage obligation, not a license to reuse a source literal after mutation.
func (e *emitter) findProjectionWrites(file *syntax.File) {
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BashPPAssign:
			for _, name := range n.Names {
				e.writtenNames[name.Value] = true
			}
		case *syntax.BashPPForAssign:
			e.writtenNames[n.Name.Value] = true
		case *syntax.CallExpr:
			for _, assignment := range n.Assigns {
				if assignment.Name != nil {
					e.writtenNames[assignment.Name.Value] = true
				}
			}
		}
		return true
	})
}
