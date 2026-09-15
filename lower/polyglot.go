package lower

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/polyglot"
	"mvdan.cc/sh/v3/syntax"
)

type foreignFunction struct {
	plan   int
	export polyglot.Export
	alias  string
}

func (e *emitter) prepareForeign(ctx context.Context, file *syntax.File) error {
	var blocks []polyglot.Block
	var first *syntax.SourceBlock
	var imports []*syntax.BashPPImport
	for _, stmt := range file.Stmts {
		switch node := stmt.Cmd.(type) {
		case *syntax.SourceBlock:
			if first == nil {
				first = node
			}
			alias := ""
			if node.Alias != nil {
				alias = node.Alias.Value
			}
			blocks = append(blocks, polyglot.Block{Language: node.Language.Value, Alias: alias, Source: node.Body, Filename: file.Name, Line: int(node.BodyPos.Line())})
		case *syntax.BashPPImport:
			if node.Language != nil {
				imports = append(imports, node)
			}
		}
	}
	if len(blocks) == 0 && len(imports) == 0 {
		return nil
	}
	reserved := map[string]string{}
	for _, stmt := range file.Stmts {
		switch n := stmt.Cmd.(type) {
		case *syntax.FuncDecl:
			reserved[n.Name.Value] = "shell function"
		case *syntax.BashPPFuncDecl:
			if n.Receiver == nil {
				reserved[n.Name.Value] = "function"
			}
		case *syntax.BashPPDecl:
			if n.Name != nil {
				reserved[n.Name.Value] = "declaration"
			}
		case *syntax.BashPPImport:
			if n.Language == nil && n.Alias != nil && n.Alias.Value != "_" && n.Alias.Value != "." {
				reserved[n.Alias.Value] = "Go import"
			}
		}
	}
	source := e.sourceName
	if source == "" {
		source = filepath.Join(e.options.Dir, ".bashpp-input")
	} else if !filepath.IsAbs(source) && e.options.Dir != "" {
		source = filepath.Join(e.options.Dir, source)
	}
	for _, imp := range imports {
		module, err := strconv.Unquote(`"` + imp.Path.Parts[0].(*syntax.Lit).Value + `"`)
		if err != nil {
			return e.fail(imp, CodeType, "invalid Python import path")
		}
		alias, ok := syntax.BashPPDerivedImportAlias(module)
		if imp.Alias != nil {
			alias, ok = imp.Alias.Value, true
		}
		if !ok {
			return e.fail(imp, CodeType, "Python import requires an explicit alias")
		}
		if kind := reserved[alias]; kind != "" {
			return e.fail(imp, CodeType, "Python import alias "+alias+" collides with "+kind)
		}
		environment := ""
		if imp.Environment != nil {
			environment = imp.Environment.Value
		}
		plan, err := polyglot.PlanImport(polyglot.ImportRequest{
			Source: source, Language: imp.Language.Value, Environment: environment, Module: module, Alias: alias,
		})
		if err != nil {
			return e.fail(imp, CodeUnsupported, err.Error())
		}
		e.foreignImports = append(e.foreignImports, plan)
		e.foreignImportAliases[alias] = len(e.foreignImports) - 1
		reserved[alias] = "Python import"
	}
	pythonRuntime := polyglot.Python{}
	typeScriptRuntime := polyglot.TypeScript{}
	for _, block := range blocks {
		language := polyglot.CanonicalLanguage(block.Language)
		if language == "python" || language == "typescript" {
			environment, err := polyglot.DiscoverEnvironment(polyglot.EnvironmentRequest{Source: source, Language: language})
			if err != nil {
				return e.fail(first, CodeUnsupported, err.Error())
			}
			if language == "python" {
				e.foreignPythonEnv = &environment
				pythonRuntime.Environment = &environment
			} else {
				e.foreignTypeScriptEnv = &environment
				typeScriptRuntime.Environment = &environment
			}
		}
	}
	var plans []polyglot.Plan
	if len(blocks) > 0 {
		var err error
		plans, err = polyglot.Prepare(ctx, blocks, map[string]polyglot.Analyzer{
			"python": pythonRuntime, "typescript": typeScriptRuntime,
		})
		if err != nil {
			return e.fail(first, CodeUnsupported, err.Error())
		}
	}
	for pi, plan := range plans {
		if plan.Alias != "" {
			if kind := reserved[plan.Alias]; kind != "" {
				return e.fail(first, CodeType, "polyglot alias "+plan.Alias+" collides with "+kind)
			}
			reserved[plan.Alias] = "foreign module"
		}
		for _, export := range plan.Exports {
			name := export.Name
			if plan.Alias != "" {
				name = plan.Alias + "." + name
			}
			if kind := reserved[name]; kind != "" {
				return e.fail(first, CodeType, "polyglot callable "+name+" collides with "+kind)
			}
			if _, exists := e.foreignFunctions[name]; exists {
				return e.fail(first, CodeType, "polyglot callable "+name+" is exported more than once")
			}
			e.foreignFunctions[name] = foreignFunction{plan: pi, export: export, alias: plan.Alias}
			reserved[name] = "foreign callable"
			if plan.Alias == "" {
				decl := lowerForeignDecl(export)
				e.funcs[name] = true
				e.functionDecls[name] = decl
			}
		}
	}
	e.foreignPlans = plans
	if len(plans) > 0 || len(imports) > 0 {
		e.bridge = true
		e.output = true
	}
	return nil
}

func (e *emitter) findPythonValues(file *syntax.File) {
	for range 3 {
		syntax.Walk(file, func(node syntax.Node) bool {
			d, ok := node.(*syntax.BashPPShortDecl)
			if !ok || len(d.Lhs) != 1 {
				return true
			}
			if d.Call != nil && e.pythonCallKind(d.Call) != "" || d.Expr != nil && e.pythonExpr(d.Expr) {
				e.pythonValues[d.Lhs[0].Value] = true
			}
			return true
		})
	}
}

func (e *emitter) pythonCallKind(call *syntax.BashPPCall) string {
	if call == nil || len(call.Fun) == 0 {
		return ""
	}
	if len(call.Fun) == 2 {
		if _, ok := e.foreignImportAliases[call.Fun[0].Value]; ok {
			return "module"
		}
	}
	if e.pythonValues[call.Fun[0].Value] {
		if len(call.Fun) == 1 {
			return "handle"
		}
		return "method"
	}
	return ""
}

func (e *emitter) pythonExpr(expr syntax.BashPPExpr) bool {
	switch x := expr.(type) {
	case *syntax.BashPPIdent:
		return e.pythonValues[x.Name.Value]
	case *syntax.BashPPSelectorExpr:
		return e.pythonExpr(x.X)
	}
	return false
}

func (e *emitter) pythonCall(call *syntax.BashPPCall) (string, error) {
	kind := e.pythonCallKind(call)
	if kind == "" {
		return "", e.fail(call, CodeUndefined, "unresolved Python call")
	}
	values := make([]string, len(call.Args))
	for i := range call.Args {
		value, err := e.callArgument(call, i)
		if err != nil {
			return "", err
		}
		values[i] = value
	}
	positional := len(values) - len(call.ArgNames)
	args := "[]any{" + strings.Join(values[:positional], ",") + "}"
	kwargs := "map[string]any{"
	for i, name := range call.ArgNames {
		if i > 0 {
			kwargs += ","
		}
		kwargs += strconv.Quote(name.Value) + ":" + values[positional+i]
	}
	kwargs += "}"
	ctx := e.prefix + "context.Background()"
	var operation string
	switch kind {
	case "module":
		index := e.foreignImportAliases[call.Fun[0].Value]
		operation = fmt.Sprintf("%spython%d.CallKeywords(%s,%s,%s,%s)", e.prefix, index, ctx, strconv.Quote(call.Fun[1].Value), args, kwargs)
	case "handle":
		receiver := e.goName(call.Fun[0].Value)
		operation = fmt.Sprintf("%spythonHandle(%s).Call(%s,%s,%s)", e.prefix, receiver, ctx, args, kwargs)
	case "method":
		receiver := e.goName(call.Fun[0].Value)
		for _, part := range call.Fun[1 : len(call.Fun)-1] {
			receiver = fmt.Sprintf("%spythonValue(%spythonHandle(%s).GetAttr(%s,%s))", e.prefix, e.prefix, receiver, ctx, strconv.Quote(part.Value))
		}
		operation = fmt.Sprintf("%spythonHandle(%s).CallAttr(%s,%s,%s,%s)", e.prefix, receiver, ctx, strconv.Quote(call.Fun[len(call.Fun)-1].Value), args, kwargs)
	}
	return e.prefix + "pythonValue(" + operation + ")", nil
}

func (e *emitter) pythonAttr(selector *syntax.BashPPSelectorExpr) (string, error) {
	receiver, err := e.expr(selector.X)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%spythonValue(%spythonHandle(%s).GetAttr(%scontext.Background(),%s))", e.prefix, e.prefix, receiver, e.prefix, strconv.Quote(selector.Sel.Value)), nil
}

func lowerForeignDecl(export polyglot.Export) *syntax.BashPPFuncDecl {
	d := &syntax.BashPPFuncDecl{Name: &syntax.Lit{Value: export.Name}}
	if export.Signature.Dynamic {
		d.Params = []*syntax.BashPPField{{Names: []*syntax.Lit{{Value: "args"}}, FieldType: &syntax.Lit{Value: "any"}, Ellipsis: syntax.NewPos(0, 1, 1)}}
		d.Results = []*syntax.BashPPField{{FieldType: &syntax.Lit{Value: "any"}}, {FieldType: &syntax.Lit{Value: "error"}}}
		return d
	}
	for i, typ := range export.Signature.Params {
		d.Params = append(d.Params, &syntax.BashPPField{Names: []*syntax.Lit{{Value: fmt.Sprintf("arg%d", i)}}, FieldType: &syntax.Lit{Value: typ}})
	}
	for _, typ := range export.Signature.Results {
		d.Results = append(d.Results, &syntax.BashPPField{FieldType: &syntax.Lit{Value: typ}})
	}
	return d
}

func (e *emitter) foreignDeclarations() string {
	if len(e.foreignPlans) == 0 && len(e.foreignImports) == 0 {
		return ""
	}
	var out strings.Builder
	for i, plan := range e.foreignImports {
		fmt.Fprintf(&out, "var %spython%d = %spolyglot.StartImport(%spolyglot.ImportPlan{ID:%s,Language:%s,Module:%s,Alias:%s,Environment:*%s})\n", e.prefix, i, e.prefix, e.prefix, strconv.Quote(plan.ID), strconv.Quote(plan.Language), strconv.Quote(plan.Module), strconv.Quote(plan.Alias), e.environmentLiteral(&plan.Environment))
	}
	if len(e.foreignImports) > 0 {
		fmt.Fprintf(&out, "var _ = %scontext.Background\n", e.prefix)
		fmt.Fprintf(&out, "func %spythonValue(result %spolyglot.CallResult, err error) any { if result.Stdout != \"\" { %sfmt.Fprint(%srt.Stdout,result.Stdout) }; if result.Stderr != \"\" { %sfmt.Fprint(%srt.Stderr,result.Stderr) }; if err != nil { panic(%srt.ValueAbort{Err:err}) }; return result.Value }\n", e.prefix, e.prefix, e.prefix, e.prefix, e.prefix, e.prefix, e.prefix)
		fmt.Fprintf(&out, "func %spythonHandle(value any) *%spolyglot.Handle { handle,ok:=value.(*%spolyglot.Handle); if !ok { panic(%srt.ValueAbort{Err:%sfmt.Errorf(\"Python value %%T is not an object\",value)}) }; return handle }\n", e.prefix, e.prefix, e.prefix, e.prefix, e.prefix)
	}
	for i, plan := range e.foreignPlans {
		module := fmt.Sprintf("%sforeign%d", e.prefix, i)
		runtime := fmt.Sprintf("%spolyglot.Python{Environment:%s}", e.prefix, e.foreignEnvironment())
		if plan.Language == "typescript" {
			runtime = fmt.Sprintf("%spolyglot.TypeScript{Environment:%s}", e.prefix, e.environmentLiteral(e.foreignTypeScriptEnv))
		}
		fmt.Fprintf(&out, "var %s = %spolyglot.Start(%spolyglot.Plan{ID:%s,Language:%s,Alias:%s,Source:%s,Artifact:%s,Exports:%s}, %s)\n", module, e.prefix, e.prefix, strconv.Quote(plan.ID), strconv.Quote(plan.Language), strconv.Quote(plan.Alias), strconv.Quote(plan.Source), strconv.Quote(plan.Artifact), e.foreignExports(plan.Exports), runtime)
		if plan.Alias != "" {
			typ := fmt.Sprintf("%sforeignModule%d", e.prefix, i)
			fmt.Fprintf(&out, "type %s struct{}\nvar %s %s\n", typ, plan.Alias, typ)
			for _, export := range plan.Exports {
				out.WriteString(e.foreignWrapper("("+plan.Alias+" "+typ+") ", module, export))
			}
		} else {
			for _, export := range plan.Exports {
				out.WriteString(e.foreignWrapper("", module, export))
			}
		}
	}
	return out.String()
}

func (e *emitter) foreignEnvironment() string {
	p := e.foreignPythonEnv
	return e.environmentLiteral(p)
}

func (e *emitter) environmentLiteral(p *polyglot.EnvironmentPlan) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("&%spolyglot.EnvironmentPlan{Language:%s,Name:%s,Root:%s,Dir:%s,Executable:%s,Manager:%s,RuntimeConstraint:%s,Runtime:%s,CompilerModule:%s,Manifests:%#v,Locks:%#v,PythonPath:%#v,Env:%#v,Explanation:%#v,ResolutionFiles:%#v,Fingerprint:%s}",
		e.prefix, strconv.Quote(p.Language), strconv.Quote(p.Name), strconv.Quote(p.Root), strconv.Quote(p.Dir), strconv.Quote(p.Executable), strconv.Quote(p.Manager), strconv.Quote(p.RuntimeConstraint), strconv.Quote(p.Runtime), strconv.Quote(p.CompilerModule), p.Manifests, p.Locks, p.PythonPath, p.Env, p.Explanation, p.ResolutionFiles, strconv.Quote(p.Fingerprint))
}

func (e *emitter) foreignExports(exports []polyglot.Export) string {
	var out strings.Builder
	fmt.Fprintf(&out, "[]%spolyglot.Export{", e.prefix)
	for _, export := range exports {
		fmt.Fprintf(&out, "{Name:%s,Signature:%spolyglot.Signature{Params:%#v,Results:%#v,Dynamic:%t}},", strconv.Quote(export.Name), e.prefix, export.Signature.Params, export.Signature.Results, export.Signature.Dynamic)
	}
	out.WriteByte('}')
	return out.String()
}

func (e *emitter) foreignWrapper(receiver, module string, export polyglot.Export) string {
	var params []string
	if export.Signature.Dynamic {
		params = []string{"args ...any"}
	} else {
		for i, typ := range export.Signature.Params {
			params = append(params, fmt.Sprintf("arg%d %s", i, foreignGoType(typ)))
		}
	}
	var results []string
	if export.Signature.Dynamic {
		results = []string{"any", "error"}
	} else {
		for _, typ := range export.Signature.Results {
			results = append(results, foreignGoType(typ))
		}
	}
	args := "args..."
	if !export.Signature.Dynamic {
		names := make([]string, len(export.Signature.Params))
		for i := range names {
			names[i] = fmt.Sprintf("arg%d", i)
		}
		args = strings.Join(names, ",")
	}
	if args != "" {
		args = "," + args
	}
	var out strings.Builder
	fmt.Fprintf(&out, "func %s%s(%s)", receiver, export.Name, strings.Join(params, ","))
	if len(results) == 1 {
		fmt.Fprintf(&out, " %s", results[0])
	} else if len(results) > 1 {
		fmt.Fprintf(&out, " (%s)", strings.Join(results, ","))
	}
	out.WriteString(" {\n")
	fmt.Fprintf(&out, "result, err := %s.Call(%scontext.Background(), %s%s)\n", module, e.prefix, strconv.Quote(export.Name), args)
	fmt.Fprintf(&out, "if result.Stdout != \"\" { %sfmt.Fprint(%srt.Stdout, result.Stdout) }; if result.Stderr != \"\" { %sfmt.Fprint(%srt.Stderr, result.Stderr) }\n", e.prefix, e.prefix, e.prefix, e.prefix)
	if export.Signature.Dynamic {
		out.WriteString("return result.Value, err\n}\n")
		return out.String()
	}
	if len(results) == 0 {
		fmt.Fprintf(&out, "if err != nil { %srt.Fail(err) }; return\n}\n", e.prefix)
		return out.String()
	}
	zero := foreignZero(export.Signature.Results[0])
	fmt.Fprintf(&out, "if err != nil { %srt.Fail(err); return %s }; return %s\n}\n", e.prefix, zero, foreignResultExpr("result.Value", export.Signature.Results[0]))
	return out.String()
}

func foreignGoType(typ string) string {
	if typ == "bytes" {
		return "[]byte"
	}
	if typ == "nil" {
		return "any"
	}
	return typ
}
func foreignZero(typ string) string {
	switch typ {
	case "string":
		return `""`
	case "bool":
		return "false"
	case "bytes":
		return "nil"
	case "float64":
		return "0"
	case "int":
		return "0"
	}
	return "nil"
}
func foreignResultExpr(value, typ string) string {
	switch typ {
	case "int":
		return "int(" + value + ".(int64))"
	case "bytes":
		return value + ".([]byte)"
	case "float64":
		return value + ".(float64)"
	case "bool":
		return value + ".(bool)"
	case "string":
		return value + ".(string)"
	}
	return value
}
