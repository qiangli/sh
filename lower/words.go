package lower

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Word-backed arguments occur only after the parser has selected a typed call
// or return. This decodes that expression; it never chooses a source dialect.
func (e *emitter) wordSequence(ws []*syntax.Word) (string, error) {
	if len(ws) == 0 {
		return "", e.fail(nil, CodeExpr, "missing value")
	}
	if len(ws) == 1 {
		return e.valueWord(ws[0])
	}
	var texts []string
	for _, w := range ws {
		if len(w.Parts) != 1 {
			return "", e.fail(w, CodeExpr, "multiword typed expression contains expansion")
		}
		l, ok := w.Parts[0].(*syntax.Lit)
		if !ok {
			return "", e.fail(w, CodeExpr, "multiword typed expression contains quoting")
		}
		texts = append(texts, l.Value)
	}
	return e.nativeWordExpr(ws[0], strings.Join(texts, " "))
}
func (e *emitter) valueWord(w *syntax.Word) (string, error) {
	if w == nil {
		return "", e.fail(nil, CodeExpr, "missing value word")
	}
	if len(w.Parts) != 1 {
		return e.shellWord(w)
	}
	switch p := w.Parts[0].(type) {
	case *syntax.Lit:
		return e.nativeWordExpr(w, p.Value)
	case *syntax.SglQuoted:
		if p.Dollar {
			return "", e.fail(w, CodeUnsupported, "ANSI-C quoting")
		}
		return strconv.Quote(p.Value), nil
	case *syntax.DblQuoted:
		return e.quoted(p)
	case *syntax.ArithmExp:
		return e.arithmetic(p.X)
	case *syntax.ParamExp:
		return e.parameter(p)
	default:
		return "", e.fail(w, CodeExpr, "unsupported typed value word")
	}
}
func (e *emitter) nativeWordExpr(n syntax.Node, text string) (string, error) {
	x, err := parser.ParseExpr(text)
	if err != nil {
		return "", e.fail(n, CodeExpr, "invalid committed expression: "+text)
	}
	var problem error
	ast.Inspect(x, func(a ast.Node) bool {
		if a == nil || problem != nil {
			return false
		}
		switch q := a.(type) {
		case *ast.Ident:
			if !e.known(q.Name) && !scalarType(q.Name) && q.Name != "print" && q.Name != "println" {
				problem = e.fail(n, CodeUndefined, "undefined: "+q.Name)
				return false
			}
		case *ast.BasicLit:
			if q.Kind != token.INT && q.Kind != token.FLOAT && q.Kind != token.STRING && q.Kind != token.CHAR {
				problem = e.fail(n, CodeUnsupported, "literal kind "+q.Kind.String())
			}
		case *ast.ParenExpr, *ast.BinaryExpr:
		case *ast.UnaryExpr:
			if q.Op != token.ADD && q.Op != token.SUB && q.Op != token.NOT && q.Op != token.XOR {
				problem = e.fail(n, CodeUnsupported, "unary word expression operator "+q.Op.String())
			}
		case *ast.CallExpr:
			if id, ok := q.Fun.(*ast.Ident); !ok || (!e.funcs[id.Name] && !scalarType(id.Name)) {
				problem = e.fail(n, CodeUnsupported, "word call requires resolved native function")
			}
		default:
			problem = e.fail(n, CodeUnsupported, fmt.Sprintf("committed word expression %T", a))
		}
		return problem == nil
	})
	if problem != nil {
		return "", problem
	}
	return text, nil
}
func (e *emitter) parameter(p *syntax.ParamExp) (string, error) {
	// Only the simple $name and ${name} bridge is implemented. All modifiers,
	// subscripts, positional and special parameters remain explicit failures.
	if p.Param == nil || !syntax.BashPPValidIdent(p.Param.Value) || p.Excl || p.Length || p.Width || p.IsSet || p.Index != nil || p.Slice != nil || p.Repl != nil || p.Exp != nil || p.NestedParam != nil || p.Flags != nil || p.Split != syntax.OptUnset || p.GlobSubst != syntax.OptUnset || p.RcExpand != syntax.OptUnset || len(p.Modifiers) > 0 || p.Names != 0 || p.BadSubst != nil {
		return "", e.fail(p, CodeBridge, "parameter expansion requires shell-state lowering")
	}
	if !e.known(p.Param.Value) {
		return "", e.fail(p, CodeUndefined, "unknown shell/typed binding: "+p.Param.Value)
	}
	return p.Param.Value, nil
}
func (e *emitter) quoted(q *syntax.DblQuoted) (string, error) {
	if q.Dollar {
		return "", e.fail(q, CodeUnsupported, "locale quoting")
	}
	return e.stringParts(q.Parts)
}
func (e *emitter) stringParts(parts []syntax.WordPart) (string, error) {
	var out []string
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			out = append(out, strconv.Quote(p.Value))
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", e.fail(p, CodeUnsupported, "ANSI-C quoting")
			}
			out = append(out, strconv.Quote(p.Value))
		case *syntax.DblQuoted:
			x, err := e.quoted(p)
			if err != nil {
				return "", err
			}
			out = append(out, x)
		case *syntax.ParamExp:
			x, err := e.parameter(p)
			if err != nil {
				return "", err
			}
			e.bridge = true
			out = append(out, e.prefix+"rt.Word("+x+")")
		case *syntax.ArithmExp:
			x, err := e.arithmetic(p.X)
			if err != nil {
				return "", err
			}
			e.bridge = true
			out = append(out, e.prefix+"rt.Word("+x+")")
		default:
			return "", e.fail(part, CodeBridge, "unsupported shell word expansion")
		}
	}
	if len(out) == 0 {
		return `""`, nil
	}
	return "(" + strings.Join(out, " + ") + ")", nil
}
func (e *emitter) shellWord(w *syntax.Word) (string, error) {
	if len(w.Parts) == 1 {
		if l, ok := w.Parts[0].(*syntax.Lit); ok && e.inFunc && e.known(l.Value) {
			return l.Value, nil
		}
	}
	return e.stringParts(w.Parts)
}
func (e *emitter) arithmetic(x syntax.ArithmExpr) (string, error) {
	switch n := x.(type) {
	case *syntax.Word:
		return e.wordSequence([]*syntax.Word{n})
	case *syntax.BinaryArithm:
		op := n.Op.String()
		switch op {
		case "+", "-", "*", "/", "%", "&", "|", "^", "<<", ">>":
		default:
			return "", e.fail(n, CodeUnsupported, "shell arithmetic operator "+op)
		}
		l, err := e.arithmetic(n.X)
		if err != nil {
			return "", err
		}
		r, err := e.arithmetic(n.Y)
		return "(" + l + " " + op + " " + r + ")", err
	case *syntax.UnaryArithm:
		op := n.Op.String()
		if op != "+" && op != "-" && op != "~" {
			return "", e.fail(n, CodeUnsupported, "shell arithmetic unary operator "+op)
		}
		if op == "~" {
			op = "^"
		}
		v, err := e.arithmetic(n.X)
		return "(" + op + v + ")", err
	case *syntax.ParenArithm:
		v, err := e.arithmetic(n.X)
		return "(" + v + ")", err
	default:
		return "", e.fail(x, CodeUnsupported, "shell arithmetic form")
	}
}
func (e *emitter) shell(c *syntax.CallExpr) (string, error) {
	if len(c.Assigns) > 0 {
		if len(c.Args) > 0 {
			return "", e.fail(c, CodeBridge, "command-scoped assignments need shell-state runtime")
		}
		var out []string
		for _, a := range c.Assigns {
			if a.Name == nil || a.Index != nil || a.Array != nil || a.Append || a.Naked {
				return "", e.fail(a, CodeBridge, "non-scalar shell assignment")
			}
			name := a.Name.Value
			if !e.known(name) {
				return "", e.fail(a, CodeBridge, "new shell binding needs shell-state runtime")
			}
			v, err := e.valueWord(a.Value)
			if err != nil {
				return "", err
			}
			out = append(out, name+" = "+v)
		}
		return strings.Join(out, "\n"), nil
	}
	if len(c.Args) == 0 {
		return "", nil
	}
	if len(c.Args[0].Parts) != 1 {
		return "", e.fail(c, CodeBridge, "dynamic command name")
	}
	lit, ok := c.Args[0].Parts[0].(*syntax.Lit)
	if !ok {
		return "", e.fail(c, CodeBridge, "quoted command name")
	}
	name := lit.Value
	if name != "printf" && name != "echo" {
		return "", e.fail(c, CodeBridge, "dynamic shell command requires runtime lowering: "+name)
	}
	if name == "printf" && len(c.Args) > 1 {
		if err := e.printfFormat(c.Args[1]); err != nil {
			return "", err
		}
	}
	e.bridge = true
	var args []string
	for _, w := range c.Args[1:] {
		s, err := e.shellWord(w)
		if err != nil {
			return "", err
		}
		args = append(args, s)
	}
	if name == "printf" && len(args) == 0 {
		return "", e.fail(c, CodeBridge, "printf requires a format")
	}
	if name == "echo" && len(c.Args) > 1 {
		w := c.Args[1]
		if len(w.Parts) == 1 {
			if l, ok := w.Parts[0].(*syntax.Lit); ok && strings.HasPrefix(l.Value, "-") {
				return "", e.fail(w, CodeBridge, "echo option parsing needs full shell runtime")
			}
		}
	}
	method := "Printf"
	if name == "echo" {
		method = "Echo"
	}
	return "if " + e.prefix + "err := " + e.prefix + "rt." + method + "(" + strings.Join(args, ", ") + "); " + e.prefix + "err != nil { " + e.prefix + "rt.Fail(" + e.prefix + "err) }", nil
}

func (e *emitter) printfFormat(w *syntax.Word) error {
	if len(w.Parts) != 1 {
		return e.fail(w, CodeBridge, "dynamic printf format needs full shell runtime")
	}
	var text string
	switch p := w.Parts[0].(type) {
	case *syntax.SglQuoted:
		if p.Dollar {
			return e.fail(w, CodeBridge, "ANSI-C format")
		}
		text = p.Value
	case *syntax.Lit:
		if e.inFunc && e.known(p.Value) {
			return e.fail(w, CodeBridge, "dynamic printf format")
		}
		text = p.Value
	default:
		return e.fail(w, CodeBridge, "foundation printf format must be a literal or single-quoted word")
	}
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			if i >= len(text) || !strings.ContainsRune("ntr\\abfv", rune(text[i])) {
				return e.fail(w, CodeBridge, "printf escape needs full shell runtime")
			}
			continue
		}
		if text[i] == '%' {
			i++
			if i >= len(text) || !strings.ContainsRune("sd%", rune(text[i])) {
				return e.fail(w, CodeBridge, "printf conversion needs full shell runtime")
			}
		}
	}
	return nil
}
