package lower

import (
	"fmt"
	"go/importer"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (e *emitter) goName(name string) string {
	if e.execution && e.funcs[name] {
		return e.prefix + "call_" + name
	}
	if name == "main" && e.funcs[name] {
		return e.prefix + "sourceMain"
	}
	return name
}

func (e *emitter) literal(f *syntax.BashPPFuncLit) (string, error) {
	previousProgram := e.programExpr
	e.programExpr = ""
	defer func() { e.programExpr = previousProgram }()
	saved := e.inFunc
	e.inFunc = true
	defer func() { e.inFunc = saved }()
	e.push()
	defer e.pop()
	signature, err := e.signature(f.Params, f.Results, f.Body)
	if err != nil {
		return "", err
	}
	savedResults := e.resultTypes
	e.resultTypes = e.returnTypes(f.Results)
	defer func() { e.resultTypes = savedResults }()
	body, err := e.block(f.Body)
	if err != nil {
		return "", err
	}
	if e.execution {
		entry, err := e.programEntry("func", false, f.Results)
		if err != nil {
			return "", err
		}
		body = entry + body
		signature = e.privateSignature(signature)
	}
	return "func" + signature + " {\n" + body + "}", nil
}
func (e *emitter) signature(params, results []*syntax.BashPPField, body *syntax.Block) (string, error) {
	p, err := e.fields(params)
	if err != nil {
		return "", err
	}
	var r []string
	for _, field := range results {
		if field.FieldType != nil && field.FieldType.Value == "func" {
			var literal *syntax.BashPPFuncLit
			syntax.Walk(body, func(n syntax.Node) bool {
				if ret, ok := n.(*syntax.BashPPReturn); ok && ret.FuncLit != nil && literal == nil {
					literal = ret.FuncLit
					return false
				}
				return true
			})
			if literal == nil {
				return "", e.fail(field, CodeUnsupported, "func result needs an inferable returned literal")
			}
			// Signature inference must not bind the nested callable's parameters in
			// its factory. The result remains an ordinary typed Go function value.
			e.push()
			sig, err := e.signature(literal.Params, literal.Results, literal.Body)
			e.pop()
			if err != nil {
				return "", err
			}
			names := names(field.Names)
			for _, name := range names {
				e.bind(name)
			}
			prefix := ""
			if len(names) > 0 {
				prefix = strings.Join(names, ",") + " "
			}
			r = append(r, prefix+"func"+sig)
		} else {
			x, err := e.fields([]*syntax.BashPPField{field})
			if err != nil {
				return "", err
			}
			r = append(r, x)
		}
	}
	result := ""
	if len(r) > 0 {
		result = " (" + strings.Join(r, ",") + ")"
	}
	return "(" + p + ")" + result, nil
}

// globalStatement separates storage from source-ordered initialization. The
// first static pass infers native types; the second emits only zero storage
// globally and performs each initialization at its original entry position.
func (e *emitter) globalStatement(s *syntax.Stmt) (string, error) {
	if err := e.statementFlags(s); err != nil {
		return "", err
	}
	switch n := s.Cmd.(type) {
	case *syntax.BashPPDecl:
		e.visibleGlobals[n.Name.Value] = true
	case *syntax.BashPPShortDecl:
		for _, name := range names(n.Lhs) {
			e.visibleGlobals[name] = true
		}
	}
	text, err := e.command(s.Cmd)
	if err != nil {
		return "", err
	}
	line := strings.SplitN(text, "\n_ = ", 2)[0]
	var ns []string
	constant := false
	switch n := s.Cmd.(type) {
	case *syntax.BashPPDecl:
		ns = []string{n.Name.Value}
		constant = n.Kw.Value == "const"
	case *syntax.BashPPShortDecl:
		ns = names(n.Lhs)
		line = "var " + strings.Replace(line, " := ", " = ", 1)
	default:
		return "", e.fail(s, CodeUnsupported, "global declaration")
	}
	newNames := map[string]bool{}
	for _, name := range ns {
		if name != "_" && !e.declaredGlobals[name] {
			newNames[name] = true
		}
	}
	if _, short := s.Cmd.(*syntax.BashPPShortDecl); short && len(newNames) == 0 {
		return "", e.fail(s, CodeType, "no new variables on left side of :=")
	}
	for _, name := range ns {
		if name != "_" {
			e.declaredGlobals[name] = true
		}
	}
	if constant {
		e.globalDecls.WriteString(e.mark(s.Cmd) + line + "\n")
		return e.mark(s.Cmd) + e.unused(ns) + "\n", nil
	}
	if e.globalTypes == nil {
		if len(newNames) == len(ns) {
			e.globalDecls.WriteString(e.mark(s.Cmd) + line + "\n")
		} else {
			eq := strings.Index(line, " = ")
			if eq < 0 {
				return "", e.fail(s, CodeType, "redeclaration needs an initializer")
			}
			var temp []string
			for i := range ns {
				temp = append(temp, fmt.Sprintf("%stuple%d_%d", e.prefix, len(e.marks), i))
			}
			fmt.Fprintf(&e.globalDecls, "%svar %s = %s\n", e.mark(s.Cmd), strings.Join(temp, ","), line[eq+3:])
			for i, name := range ns {
				if newNames[name] {
					fmt.Fprintf(&e.globalDecls, "var %s = %s\n", name, temp[i])
				}
			}
		}
		return e.mark(s.Cmd) + e.unused(ns) + "\n", nil
	}
	for _, name := range ns {
		if !newNames[name] {
			continue
		}
		typ, ok := e.globalTypes[name]
		if !ok {
			return "", e.fail(s, CodeType, "missing inferred type for "+name)
		}
		fmt.Fprintf(&e.globalDecls, "%svar %s %s\n", e.mark(s.Cmd), name, typ)
	}

	eq := strings.Index(line, " = ")
	if eq < 0 {
		return e.mark(s.Cmd) + e.unused(ns) + "\n", nil
	}
	return e.mark(s.Cmd) + "/*" + e.prefix + "reset*/" + strings.Join(ns, ", ") + " = " + line[eq+3:] + "\n", nil
}

func (e *emitter) switchStmt(n *syntax.BashPPSwitch) (string, error) {
	if n.TypeSwitch {
		return e.typeSwitchStmt(n)
	}
	e.push()
	defer e.pop()
	init := ""
	var err error
	if n.Init != nil {
		init, err = e.command(n.Init)
		if err != nil {
			return "", err
		}
	}
	tag := ""
	if n.Tag != nil {
		tag, err = e.expr(n.Tag)
		if err != nil {
			return "", err
		}
	}
	var out strings.Builder
	out.WriteString("switch " + tag + " {\n")
	for _, arm := range n.Arms {
		e.push()
		if len(arm.Exprs) == 0 {
			out.WriteString("default:\n")
		} else {
			var cases []string
			for _, expr := range arm.Exprs {
				x, err := e.expr(expr)
				if err != nil {
					e.pop()
					return "", err
				}
				cases = append(cases, x)
			}
			out.WriteString("case " + strings.Join(cases, ",") + ":\n")
		}
		for _, s := range arm.Stmts {
			x, err := e.statement(s)
			if err != nil {
				e.pop()
				return "", err
			}
			out.WriteString(x)
		}
		e.pop()
	}
	if e.completeEnumSwitch(n) {
		out.WriteString("default: panic(\"invalid enum value\")\n")
	}
	out.WriteString("}")
	if init != "" {
		return "{\n" + init + "\n" + out.String() + "\n}", nil
	}
	return out.String(), nil
}

func (e *emitter) isRecover(c *syntax.BashPPCall) bool {
	return c != nil && len(c.Fun) == 1 && c.Fun[0].Value == "recover" && !e.funcs["recover"]
}
func (e *emitter) recovered(name string) string {
	if e.execution {
		return "\n" + name + " = " + e.program() + ".Recovered(" + name + ")"
	}
	return "\nif " + name + " == nil { " + name + " = \"\"; /*" + e.prefix + "status1*/ } else { " + e.prefix + "popPanic(); /*" + e.prefix + "status0*/ }"
}
func (e *emitter) panicHelpers() string {
	return strings.ReplaceAll(`
var PREFIXpanicChain []string
func PREFIXpushPanic(v any) string {s:=PREFIXfmt.Sprint(v);PREFIXpanicChain=append(PREFIXpanicChain,s);return s}
func PREFIXpopPanic(){if len(PREFIXpanicChain)>0{PREFIXpanicChain=PREFIXpanicChain[:len(PREFIXpanicChain)-1]}}
`, "PREFIX", e.prefix)
}
func (e *emitter) panicBoundary() string {
	return strings.ReplaceAll(`defer func(){if v:=recover();v!=nil{if len(PREFIXpanicChain)==0{PREFIXpanicChain=append(PREFIXpanicChain,PREFIXfmt.Sprint(v))};for i,message:=range PREFIXpanicChain{if i>0{PREFIXfmt.Fprint(PREFIXos.Stderr,"\t")};PREFIXfmt.Fprintln(PREFIXos.Stderr,"panic:",message)};PREFIXos.Exit(2)}}()
`, "PREFIX", e.prefix)
}

func (e *emitter) importDecl(n *syntax.BashPPImport) error {
	specs := n.Specs
	if n.Path != nil {
		specs = []*syntax.BashPPImportSpec{{Alias: n.Alias, Path: n.Path}}
	}
	for _, spec := range specs {
		var path strings.Builder
		for _, part := range spec.Path.Parts {
			lit, ok := part.(*syntax.Lit)
			if !ok {
				return e.fail(spec, CodeType, "nonliteral import path")
			}
			path.WriteString(lit.Value)
		}
		p := path.String()
		if !syntax.BashPPStdlibImportAllowed(p) {
			return e.fail(spec, CodeUnsupported, "external import needs module-aware compile resolution: "+p)
		}
		pkg, err := importer.Default().Import(p)
		if err != nil {
			return e.fail(spec, CodeType, err.Error())
		}
		alias := pkg.Name()
		if spec.Alias != nil {
			alias = spec.Alias.Value
		}
		if alias == "." {
			for _, name := range pkg.Scope().Names() {
				if pkg.Scope().Lookup(name).Exported() {
					e.dotNames[name] = true
				}
			}
		}
		e.imports[alias] = p
		e.bind(alias)
	}
	return nil
}

func (e *emitter) constGroup(n *syntax.BashPPConstGroup) (string, error) {
	e.push()
	e.bind("iota")
	defer e.pop()
	saved := e.iotaValue
	defer func() { e.iotaValue = saved }()
	shadowed := false
	var lines []string
	var lastExpr syntax.BashPPExpr
	var lastWords []*syntax.Word
	var lastType string
	for _, spec := range n.Specs {
		current := int(spec.Iota)
		if shadowed {
			e.iotaValue = nil
		} else {
			e.iotaValue = &current
		}
		typ := ""
		var err error
		if spec.DeclTypeExpr != nil {
			typ, err = e.typeExpr(spec.DeclTypeExpr)
		} else if spec.DeclType != nil {
			typ, err = e.typeSpelling(spec.DeclType, spec.DeclType.Value)
		}
		if err != nil {
			return "", err
		}
		expr, words := spec.InitExpr, spec.Init
		if expr == nil && len(words) == 0 {
			expr, words, typ = lastExpr, lastWords, lastType
		} else {
			lastExpr, lastWords, lastType = expr, words, typ
		}
		value := ""
		if expr != nil {
			value, err = e.expr(expr)
		} else if len(words) > 0 {
			value, err = e.wordSequence(words)
		}
		if err != nil {
			return "", err
		}
		if typ != "" {
			typ = " " + typ
		}
		if value != "" {
			value = " = " + value
		}
		lines = append(lines, spec.Name.Value+typ+value)
		e.bind(spec.Name.Value)
		if expr != nil {
			e.projections.projectionBind(spec.Name.Value, e.projectionExpr(expr))
		} else if len(words) == 1 {
			e.projections.projectionBind(spec.Name.Value, e.projectionWord(words[0]))
		}
		if spec.Name.Value == "iota" {
			shadowed = true
		}
	}
	for _, spec := range n.Specs {
		if spec.Name.Value != "_" {
			e.scopes[len(e.scopes)-2][spec.Name.Value] = true
			if p, ok := e.projections.projectionLookup(spec.Name.Value); ok {
				e.projections.scopes[len(e.projections.scopes)-2][spec.Name.Value] = p
			}
		}
	}
	return "const (\n" + strings.Join(lines, "\n") + "\n)", nil
}

func (e *emitter) rangeStmt(n *syntax.BashPPRange) (string, error) {
	if e.execution && n.Chan != nil {
		return e.runtimeChannelRange(n, e.runtimeScope(), e.runtimeStatements)
	}
	e.push()
	defer e.pop()
	var rhs string
	var err error
	if n.Expr != nil {
		rhs, err = e.expr(n.Expr)
	} else {
		rhs, err = e.valueWord(n.Chan)
	}
	if err != nil {
		return "", err
	}
	ns := names(n.Names)
	op := " = "
	if n.Define.IsValid() {
		op = " := "
		for _, name := range ns {
			e.bind(name)
		}
	}
	prefix := ""
	if len(ns) > 0 {
		prefix = strings.Join(ns, ",") + op
	}
	body, err := e.block(n.Body)
	return "for " + prefix + "range " + rhs + " {\n" + body + "}", err
}
func (e *emitter) importLines() string {
	aliases := make([]string, 0, len(e.imports))
	for alias := range e.imports {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	var out strings.Builder
	for _, alias := range aliases {
		fmt.Fprintf(&out, "import %s %s\n", alias, strconv.Quote(e.imports[alias]))
	}
	return out.String()
}

// Bare func parameters are resolved from the concrete callable values supplied
// by this source unit. The output still uses native function types/calls. A
// genuinely dynamic or incompatible signature remains a positioned diagnostic.
func (e *emitter) discoverCallableParams(file *syntax.File) error {
	shapes := map[string]string{}
	declarations := map[string]*syntax.BashPPFuncDecl{}
	syntax.Walk(file, func(n syntax.Node) bool {
		switch x := n.(type) {
		case *syntax.BashPPFuncDecl:
			if x.Receiver == nil {
				declarations[x.Name.Value] = x
				if shape, err := e.callbackShape(x.Params, x.Results); err == nil {
					shapes[x.Name.Value] = shape
				}
			}
		case *syntax.BashPPShortDecl:
			if x.FuncLit != nil && len(x.Lhs) == 1 {
				if shape, err := e.callbackShape(x.FuncLit.Params, x.FuncLit.Results); err == nil {
					shapes[x.Lhs[0].Value] = shape
				}
			}
		}
		return true
	})
	var problem error
	syntax.Walk(file, func(n syntax.Node) bool {
		if problem != nil {
			return false
		}
		call, ok := n.(*syntax.BashPPCall)
		if !ok || len(call.Fun) != 1 {
			return true
		}
		decl := declarations[call.Fun[0].Value]
		if decl == nil {
			return true
		}
		index := 0
		for _, field := range decl.Params {
			count := len(field.Names)
			if count == 0 {
				count = 1
			}
			for j := 0; j < count; j++ {
				if field.FieldType != nil && field.FieldType.Value == "func" && index < len(call.Args) {
					name := callableWordName(call.Args[index])
					shape := shapes[name]
					if shape != "" {
						if previous := e.callableParams[field]; previous != "" && previous != shape {
							problem = e.fail(field, CodeType, "func parameter receives incompatible native signatures")
							return false
						}
						e.callableParams[field] = shape
					}
				}
				index++
			}
		}
		return true
	})
	return problem
}
func callableWordName(w *syntax.Word) string {
	if w == nil || len(w.Parts) != 1 {
		return ""
	}
	switch p := w.Parts[0].(type) {
	case *syntax.Lit:
		return p.Value
	case *syntax.ParamExp:
		if p.Param != nil {
			return p.Param.Value
		}
	}
	return ""
}
func (e *emitter) callbackShape(params, results []*syntax.BashPPField) (string, error) {
	parts := func(fields []*syntax.BashPPField) (string, error) {
		var out []string
		for _, f := range fields {
			typ := "any"
			var err error
			if f.FieldType != nil || f.FieldTypeExpr != nil {
				typ, err = e.fieldType(f)
			}
			if err != nil {
				return "", err
			}
			n := len(f.Names)
			if n == 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				out = append(out, typ)
			}
		}
		return strings.Join(out, ","), nil
	}
	p, err := parts(params)
	if err != nil {
		return "", err
	}
	r, err := parts(results)
	if err != nil {
		return "", err
	}
	if r != "" {
		r = " (" + r + ")"
	}
	return "func(" + p + ")" + r, nil
}

func (e *emitter) shellFor(n *syntax.ForClause) (string, error) {
	loop, ok := n.Loop.(*syntax.WordIter)
	if !ok || n.Select || len(loop.Items) != 1 {
		return "", e.fail(n, CodeUnsupported, "shell loop requires runtime lowering")
	}
	w := loop.Items[0]
	if len(w.Parts) != 1 {
		return "", e.fail(w, CodeBridge, "shell loop word expansion")
	}
	q, ok := w.Parts[0].(*syntax.DblQuoted)
	if !ok || len(q.Parts) != 1 {
		return "", e.fail(w, CodeBridge, "shell loop requires a quoted typed-slice spread")
	}
	p, ok := q.Parts[0].(*syntax.ParamExp)
	if !ok || p.Param == nil || p.Index == nil {
		return "", e.fail(w, CodeBridge, "shell loop requires a typed-slice spread")
	}
	index, ok := p.Index.(*syntax.Word)
	if !ok || index.Lit() != "@" || !e.known(p.Param.Value) {
		return "", e.fail(w, CodeBridge, "shell loop requires a typed-slice spread")
	}
	e.push()
	defer e.pop()
	e.bind(loop.Name.Value)
	var body strings.Builder
	for _, stmt := range n.Do {
		text, err := e.statement(stmt)
		if err != nil {
			return "", err
		}
		body.WriteString(text)
	}
	return "for _, " + loop.Name.Value + " := range " + p.Param.Value + " {\n" + body.String() + "}", nil
}

func (e *emitter) returnTypes(fields []*syntax.BashPPField) []string {
	var out []string
	for _, field := range fields {
		typ := ""
		if field.FieldType != nil && field.FieldType.Value != "func" {
			typ, _ = e.fieldType(field)
		}
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			out = append(out, typ)
		}
	}
	return out
}

func (e *emitter) typeSwitchStmt(n *syntax.BashPPSwitch) (string, error) {
	init, ok := n.Init.(*syntax.BashPPShortDecl)
	if !ok || len(init.Lhs) != 1 {
		return "", e.fail(n, CodeUnsupported, "type switch guard")
	}
	assert, ok := init.Expr.(*syntax.BashPPTypeAssertExpr)
	if !ok {
		return "", e.fail(n, CodeType, "missing type switch assertion")
	}
	root, err := e.expr(assert.X)
	if err != nil {
		return "", err
	}
	name := init.Lhs[0].Value
	var out strings.Builder
	out.WriteString("switch " + name + " := " + root + ".(type) {\n")
	for _, arm := range n.Arms {
		e.push()
		e.bind(name)
		e.projections.projectionBind(name, interfaceProjection())
		if len(arm.Exprs) == 0 {
			out.WriteString("default:\n")
		} else {
			var values []string
			for _, x := range arm.Exprs {
				v, err := e.expr(x)
				if err != nil {
					e.pop()
					return "", err
				}
				values = append(values, v)
			}
			if len(values) == 1 && values[0] != "nil" {
				e.projections.projectionBind(name, e.projectionType(values[0], nil))
			} else {
				e.projections.projectionBind(name, interfaceProjection())
			}
			out.WriteString("case " + strings.Join(values, ",") + ":\n")
		}
		out.WriteString("_ = " + name + "\n")
		for _, stmt := range arm.Stmts {
			text, err := e.statement(stmt)
			if err != nil {
				e.pop()
				return "", err
			}
			out.WriteString(text)
		}
		e.pop()
	}
	out.WriteString("}")
	return out.String(), nil
}
