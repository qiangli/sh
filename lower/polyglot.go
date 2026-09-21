package lower

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/polyglot"
	"mvdan.cc/sh/v3/syntax"
)

type foreignFunction struct {
	plan   int
	export polyglot.Export
	alias  string
}

func (e *emitter) hasEmbeddedShellRuntime() bool {
	for _, plan := range e.foreignPlans {
		if plan.Language == "bash" || plan.Language == "sh" {
			return true
		}
	}
	return e.seedsForeignImports()
}

// seedsForeignImports reports whether the program's shell regions must see
// its direct Python imports: a mixed unit hands them to the region backend
// through interp.ForeignImports so `alias.fn args` in a region reaches the
// program's own worker (B8), not an external lookup.
func (e *emitter) seedsForeignImports() bool {
	return e.mixedShell && len(e.foreignImports) > 0
}

// isForeignWorkerVar reports whether a generated package-level name is one of
// the direct-import worker variables emitted by foreignDeclarations.
func (e *emitter) isForeignWorkerVar(name string) bool {
	rest, ok := strings.CutPrefix(name, e.prefix+"python")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// foreignImportSeed is the shellexec option that binds the program's import
// aliases in the region backend.
func (e *emitter) foreignImportSeed() string {
	aliases := make([]string, 0, len(e.foreignImportAliases))
	for alias := range e.foreignImportAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	var out strings.Builder
	fmt.Fprintf(&out, "%sshellexec.RunnerOptions(%sinterp.ForeignImports(map[string]*%spolyglot.Module{", e.prefix, e.prefix, e.prefix)
	for i, alias := range aliases {
		if i > 0 {
			out.WriteString(",")
		}
		fmt.Fprintf(&out, "%s:%spython%d", strconv.Quote(alias), e.prefix, e.foreignImportAliases[alias])
	}
	out.WriteString("}))")
	return out.String()
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
		// One module identity is one worker however many aliases name it,
		// as the interpreter shares it (CPython: `import x as a; import x
		// as b` is one module object).
		index := -1
		for i, existing := range e.foreignImports {
			if existing.Identity() == plan.Identity() {
				index = i
				break
			}
		}
		if index < 0 {
			e.foreignImports = append(e.foreignImports, plan)
			index = len(e.foreignImports) - 1
		}
		e.foreignImportAliases[alias] = index
		reserved[alias] = "Python import"
	}
	pythonRuntime := polyglot.Python{}
	typeScriptRuntime := polyglot.TypeScript{}
	rustRuntime := polyglot.Rust{}
	cRuntime := polyglot.C{}
	cppRuntime := polyglot.CPP{}
	goRuntime := polyglot.Go{}
	bashRuntime := interp.ShellRuntime("bash", e.options.Dir, os.Environ())
	shRuntime := interp.ShellRuntime("sh", e.options.Dir, os.Environ())
	for _, block := range blocks {
		language := polyglot.CanonicalLanguage(block.Language)
		if language == "python" || language == "typescript" || language == "rust" || language == "c" || language == "cpp" || language == "go" {
			environment, err := polyglot.DiscoverEnvironment(polyglot.EnvironmentRequest{Source: source, Language: language})
			if err != nil {
				return e.fail(first, CodeUnsupported, err.Error())
			}
			if language == "python" {
				e.foreignPythonEnv = &environment
				pythonRuntime.Environment = &environment
			} else if language == "typescript" {
				e.foreignTypeScriptEnv = &environment
				typeScriptRuntime.Environment = &environment
			} else if language == "rust" {
				e.foreignRustEnv = &environment
				rustRuntime.Environment = &environment
			} else if language == "c" {
				e.foreignCEnv = &environment
				cRuntime.Environment = &environment
			} else if language == "cpp" {
				e.foreignCPPEnv = &environment
				cppRuntime.Environment = &environment
			} else {
				e.foreignGoEnv = &environment
				goRuntime.Environment = &environment
			}
		}
	}
	var plans []polyglot.Plan
	if len(blocks) > 0 {
		var err error
		plans, err = polyglot.Prepare(ctx, blocks, map[string]polyglot.Analyzer{
			"python": pythonRuntime, "typescript": typeScriptRuntime, "rust": rustRuntime, "c": cRuntime, "cpp": cppRuntime, "go": goRuntime,
			"bash": bashRuntime, "sh": shRuntime,
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
		if e.pythonModuleSelector(x) {
			return true
		}
		return e.pythonExpr(x.X)
	}
	return false
}

// pythonModuleSelector reports whether a selector reads a module attribute
// of a direct import (`mod.attr`), as opposed to an attribute of a value.
func (e *emitter) pythonModuleSelector(selector *syntax.BashPPSelectorExpr) bool {
	id, ok := selector.X.(*syntax.BashPPIdent)
	if !ok || e.pythonValues[id.Name.Value] || e.known(id.Name.Value) {
		return false
	}
	_, ok = e.foreignImportAliases[id.Name.Value]
	return ok
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
	if e.pythonModuleSelector(selector) {
		index := e.foreignImportAliases[selector.X.(*syntax.BashPPIdent).Name.Value]
		return fmt.Sprintf("%spythonValue(%spython%d.Attr(%scontext.Background(),%s))", e.prefix, e.prefix, index, e.prefix, strconv.Quote(selector.Sel.Value)), nil
	}
	receiver, err := e.expr(selector.X)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%spythonValue(%spythonHandle(%s).GetAttr(%scontext.Background(),%s))", e.prefix, e.prefix, receiver, e.prefix, strconv.Quote(selector.Sel.Value)), nil
}

func lowerForeignDecl(export polyglot.Export) *syntax.BashPPFuncDecl {
	pos := syntax.NewPos(0, 1, 1)
	d := &syntax.BashPPFuncDecl{Kw: &syntax.Lit{ValuePos: pos, Value: "func"}, Name: &syntax.Lit{ValuePos: pos, Value: export.Name}}
	if export.Signature.Dynamic {
		d.Params = []*syntax.BashPPField{{Names: []*syntax.Lit{{Value: "args"}}, FieldType: &syntax.Lit{Value: "any"}, Ellipsis: syntax.NewPos(0, 1, 1)}}
		d.Results = []*syntax.BashPPField{{FieldType: &syntax.Lit{Value: "any"}}, {FieldType: &syntax.Lit{Value: "error"}}}
		return d
	}
	for i, typ := range export.Signature.Params {
		field := &syntax.BashPPField{Names: []*syntax.Lit{{Value: fmt.Sprintf("arg%d", i)}}, FieldType: &syntax.Lit{Value: foreignShellType(typ)}}
		if export.Signature.Variadic && i == len(export.Signature.Params)-1 {
			field.Ellipsis = syntax.NewPos(0, 1, 1)
		}
		d.Params = append(d.Params, field)
	}
	for _, typ := range export.Signature.Results {
		d.Results = append(d.Results, &syntax.BashPPField{FieldType: &syntax.Lit{Value: foreignShellType(typ)}})
	}
	return d
}

func foreignShellType(typ string) string {
	switch typ {
	case "object", "handle", "callback":
		return "any"
	}
	return typ
}

func (e *emitter) foreignDeclarations() string {
	if len(e.foreignPlans) == 0 && len(e.foreignImports) == 0 {
		return ""
	}
	// Every package-level binding declared here is engine storage for a
	// foreign module, not script state: the lexical storage pass must leave
	// these declarations in place, because the generated wrappers reference
	// them from plain Go functions that hold no program scope.
	if e.foreignGlobals == nil {
		e.foreignGlobals = map[string]bool{}
	}
	var out strings.Builder
	for i, plan := range e.foreignImports {
		e.foreignGlobals[fmt.Sprintf("%spython%d", e.prefix, i)] = true
		fmt.Fprintf(&out, "var %spython%d = %spolyglot.StartImport(%spolyglot.ImportPlan{ID:%s,Language:%s,Module:%s,Alias:%s,Path:%s,Environment:*%s})\n", e.prefix, i, e.prefix, e.prefix, strconv.Quote(plan.ID), strconv.Quote(plan.Language), strconv.Quote(plan.Module), strconv.Quote(plan.Alias), strconv.Quote(plan.Path), e.environmentLiteral(&plan.Environment))
	}
	if len(e.foreignImports) > 0 {
		fmt.Fprintf(&out, "var _ = %scontext.Background\n", e.prefix)
		fmt.Fprintf(&out, "func %spythonValue(result %spolyglot.CallResult, err error) any { if result.Stdout != \"\" { %sfmt.Fprint(%srt.Stdout,result.Stdout) }; if result.Stderr != \"\" { %sfmt.Fprint(%srt.Stderr,result.Stderr) }; if err != nil { panic(%srt.ValueAbort{Err:err}) }; return result.Value }\n", e.prefix, e.prefix, e.prefix, e.prefix, e.prefix, e.prefix, e.prefix)
		fmt.Fprintf(&out, "func %spythonHandle(value any) *%spolyglot.Handle { handle,ok:=value.(*%spolyglot.Handle); if !ok { panic(%srt.ValueAbort{Err:%sfmt.Errorf(\"Python value %%T is not an object\",value)}) }; return handle }\n", e.prefix, e.prefix, e.prefix, e.prefix, e.prefix)
	}
	if len(e.foreignPlans) > 0 {
		// foreignContext is the context every foreign call runs under: the
		// program's background context, or, while a shell callback runs, the
		// callback's own context, which is what lets the callback's nested
		// foreign calls re-enter the module instead of waiting on it.
		e.foreignGlobals[e.prefix+"foreignContext"] = true
		fmt.Fprintf(&out, "var %sforeignContext = %scontext.Background()\n", e.prefix, e.prefix)
	}
	for i, plan := range e.foreignPlans {
		module := fmt.Sprintf("%sforeign%d", e.prefix, i)
		e.foreignGlobals[module] = true
		runtime := fmt.Sprintf("%spolyglot.Python{Environment:%s}", e.prefix, e.foreignEnvironment())
		if plan.Language == "typescript" {
			runtime = fmt.Sprintf("%spolyglot.TypeScript{Environment:%s}", e.prefix, e.environmentLiteral(e.foreignTypeScriptEnv))
		} else if plan.Language == "rust" {
			runtime = fmt.Sprintf("%spolyglot.Rust{Environment:%s}", e.prefix, e.environmentLiteral(e.foreignRustEnv))
		} else if plan.Language == "c" {
			runtime = fmt.Sprintf("%spolyglot.C{Environment:%s}", e.prefix, e.environmentLiteral(e.foreignCEnv))
		} else if plan.Language == "cpp" {
			runtime = fmt.Sprintf("%spolyglot.CPP{Environment:%s}", e.prefix, e.environmentLiteral(e.foreignCPPEnv))
		} else if plan.Language == "go" {
			runtime = fmt.Sprintf("%spolyglot.Go{Environment:%s}", e.prefix, e.environmentLiteral(e.foreignGoEnv))
		} else if plan.Language == "bash" || plan.Language == "sh" {
			runtime = fmt.Sprintf("%sinterp.ShellRuntime(%s,\"\",nil)", e.prefix, strconv.Quote(plan.Language))
		}
		fmt.Fprintf(&out, "var %s = %spolyglot.Start(%spolyglot.Plan{ID:%s,Language:%s,Alias:%s,Source:%s,Artifact:%s,Exports:%s}, %s)\n", module, e.prefix, e.prefix, strconv.Quote(plan.ID), strconv.Quote(plan.Language), strconv.Quote(plan.Alias), strconv.Quote(plan.Source), strconv.Quote(plan.Artifact), e.foreignExports(plan.Exports), runtime)
		if plan.Language == "rust" {
			out.WriteString(e.foreignCallbacks(i, module))
		}
		if plan.Alias != "" {
			typ := fmt.Sprintf("%sforeignModule%d", e.prefix, i)
			alias := plan.Alias
			if alias == "go" {
				alias = fmt.Sprintf("%sforeignAlias%d", e.prefix, i)
			}
			e.foreignGlobals[alias] = true
			fmt.Fprintf(&out, "type %s struct{}\nvar %s %s\n", typ, alias, typ)
			for _, export := range plan.Exports {
				out.WriteString(e.foreignWrapper("("+alias+" "+typ+") ", module, export))
				out.WriteString(e.foreignErrWrapper(i, module, plan.Alias, export))
			}
		} else {
			for _, export := range plan.Exports {
				out.WriteString(e.foreignWrapper("", module, export))
				out.WriteString(e.foreignErrWrapper(i, module, plan.Alias, export))
			}
		}
	}
	return out.String()
}

// foreignCallbacks emits a Rust module's shell callback wiring: a resolver
// over the program's own functions for callbacks passed by name, the
// callback-scoped foreignContext swap, and island output delivered to the
// program's streams before a callback runs. The interpreter's
// bashPPForeignCallbacks is the same contract.
func (e *emitter) foreignCallbacks(plan int, module string) string {
	resolver := fmt.Sprintf("%sresolveCallbacks%d", e.prefix, plan)
	names := make([]string, 0, len(e.funcs))
	for name := range e.funcs {
		if _, foreign := e.foreignFunctions[name]; foreign || e.functionDecls[name] == nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	fmt.Fprintf(&out, "func %s(name string) (%spolyglot.Callback, bool) {\nswitch name {\n", resolver, e.prefix)
	for _, name := range names {
		fmt.Fprintf(&out, "case %s:\nreturn %spolyglot.FuncCallback(name, %s), true\n", strconv.Quote(name), e.prefix, e.goName(name))
	}
	fmt.Fprintf(&out, "}\nreturn %spolyglot.Callback{}, false\n}\n", e.prefix)
	fmt.Fprintf(&out, "func init() {\n%s.SetCallbacks(%spolyglot.Callbacks{\nResolve: %s,\n", module, e.prefix, resolver)
	fmt.Fprintf(&out, "Enter: func(ctx %scontext.Context) func() { saved := %sforeignContext; %sforeignContext = ctx; return func() { %sforeignContext = saved } },\n", e.prefix, e.prefix, e.prefix, e.prefix)
	fmt.Fprintf(&out, "Output: func(stdout, stderr string) { if stdout != \"\" { %sfmt.Fprint(%srt.Stdout, stdout) }; if stderr != \"\" { %sfmt.Fprint(%srt.Stderr, stderr) } },\n})\n}\n", e.prefix, e.prefix, e.prefix, e.prefix)
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
	return fmt.Sprintf("&%spolyglot.EnvironmentPlan{Language:%s,Name:%s,Root:%s,Dir:%s,SourceDir:%s,Executable:%s,Manager:%s,RuntimeConstraint:%s,Runtime:%s,CompilerModule:%s,Manifests:%#v,Locks:%#v,PythonPath:%#v,Env:%#v,Explanation:%#v,ResolutionFiles:%#v,Fingerprint:%s}",
		e.prefix, strconv.Quote(p.Language), strconv.Quote(p.Name), strconv.Quote(p.Root), strconv.Quote(p.Dir), strconv.Quote(p.SourceDir), strconv.Quote(p.Executable), strconv.Quote(p.Manager), strconv.Quote(p.RuntimeConstraint), strconv.Quote(p.Runtime), strconv.Quote(p.CompilerModule), p.Manifests, p.Locks, p.PythonPath, p.Env, p.Explanation, p.ResolutionFiles, strconv.Quote(p.Fingerprint))
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
			if export.Signature.Variadic && i == len(export.Signature.Params)-1 {
				params = append(params, fmt.Sprintf("arg%d ...%s", i, foreignGoType(typ)))
			} else {
				params = append(params, fmt.Sprintf("arg%d %s", i, foreignGoType(typ)))
			}
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
		if export.Signature.Variadic {
			args = fmt.Sprintf("%spolyglot.StringsToAny(arg0)...", e.prefix)
		} else {
			names := make([]string, len(export.Signature.Params))
			for i := range names {
				names[i] = fmt.Sprintf("arg%d", i)
			}
			args = strings.Join(names, ",")
		}
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
	fmt.Fprintf(&out, "result, err := %s.Call(%sforeignContext, %s%s)\n", module, e.prefix, strconv.Quote(export.Name), args)
	fmt.Fprintf(&out, "if result.Stdout != \"\" { %sfmt.Fprint(%srt.Stdout, result.Stdout) }; if result.Stderr != \"\" { %sfmt.Fprint(%srt.Stderr, result.Stderr) }\n", e.prefix, e.prefix, e.prefix, e.prefix)
	if export.Signature.Dynamic {
		fmt.Fprintf(&out, "if err != nil { return result.Value, %srt.TrustedErrorText(err.Error()) }; return result.Value, nil\n}\n", e.prefix)
		return out.String()
	}
	if len(results) == 0 {
		fmt.Fprintf(&out, "if err != nil { %srt.Fail(err); %srt.Status = %srt.ExitCode(err) }; return\n}\n", e.prefix, e.prefix, e.prefix)
		return out.String()
	}
	zero := foreignZero(export.Signature.Results[0])
	fmt.Fprintf(&out, "if err != nil { %srt.Fail(err); %srt.Status = %srt.ExitCode(err); return %s }; return %s\n}\n", e.prefix, e.prefix, e.prefix, zero, foreignResultExpr("result.Value", export.Signature.Results[0]))
	return out.String()
}

func (e *emitter) foreignErrAdapterName(plan int, export polyglot.Export) string {
	return fmt.Sprintf("%sforeignErr%d_%s", e.prefix, plan, export.Name)
}

// foreignErrWrapper is the explicit error opt-in adapter emitted beside the
// legacy wrapper of every typed export: `value, err := f()` (or `err := f()`
// for a zero-result export) calls this helper instead of the wrapper. The
// worker's own failure — one carrying polyglot.ForeignErrorDetail — comes back
// as the trailing error with status 0. A transport failure (EOF from a dead
// worker, cancellation, a response ID mismatch, a decode or annotation
// violation, a launch failure) is not the function's error: it is reported
// exactly like the one-value form reports it, diagnostic and failure status,
// and the helper returns zero results with a nil error. The interpreter's
// bashPPInvokeForeignErr is the same contract.
func (e *emitter) foreignErrWrapper(plan int, module, alias string, export polyglot.Export) string {
	if export.Signature.Dynamic {
		return ""
	}
	qualified := export.Name
	if alias != "" {
		qualified = alias + "." + export.Name
	}
	var params []string
	for i, typ := range export.Signature.Params {
		if export.Signature.Variadic && i == len(export.Signature.Params)-1 {
			params = append(params, fmt.Sprintf("arg%d ...%s", i, foreignGoType(typ)))
		} else {
			params = append(params, fmt.Sprintf("arg%d %s", i, foreignGoType(typ)))
		}
	}
	args := ""
	if export.Signature.Variadic {
		args = fmt.Sprintf("%spolyglot.StringsToAny(arg0)...", e.prefix)
	} else {
		names := make([]string, len(export.Signature.Params))
		for i := range names {
			names[i] = fmt.Sprintf("arg%d", i)
		}
		args = strings.Join(names, ",")
	}
	if args != "" {
		args = "," + args
	}
	results := make([]string, len(export.Signature.Results))
	zeros := make([]string, len(results))
	returns := make([]string, len(results))
	for i, typ := range export.Signature.Results {
		results[i] = foreignGoType(typ)
		zeros[i] = foreignZero(typ)
		returns[i] = foreignResultExpr("result.Value", typ)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "func %s(%s) %s {\n", e.foreignErrAdapterName(plan, export), strings.Join(params, ","), foreignErrSignature(results))
	fmt.Fprintf(&out, "result, err := %s.Call(%sforeignContext, %s%s)\n", module, e.prefix, strconv.Quote(export.Name), args)
	fmt.Fprintf(&out, "if result.Stdout != \"\" { %sfmt.Fprint(%srt.Stdout, result.Stdout) }; if result.Stderr != \"\" { %sfmt.Fprint(%srt.Stderr, result.Stderr) }\n", e.prefix, e.prefix, e.prefix, e.prefix)
	fmt.Fprintf(&out, "if err != nil {\nif _, foreign := %spolyglot.ForeignErrorDetail(err); !foreign {\n%srt.Fail(%sfmt.Errorf(%s, err))\n%srt.Status = %srt.ExitCode(err)\nreturn %s\n}\n%srt.Status = 0\nreturn %s\n}\n",
		e.prefix, e.prefix, e.prefix, strconv.Quote("bash++: foreign call "+qualified+" failed: %w"), e.prefix, e.prefix, strings.Join(append(append([]string(nil), zeros...), "nil"), ","), e.prefix, strings.Join(append(append([]string(nil), zeros...), "err"), ","))
	fmt.Fprintf(&out, "%srt.Status = 0\nreturn %s\n}\n", e.prefix, strings.Join(append(append([]string(nil), returns...), "nil"), ","))
	return out.String()
}

// foreignErrSignature spells the adapter's result list: the export's results
// followed by the error, which is the whole list for a zero-result export.
func foreignErrSignature(results []string) string {
	if len(results) == 0 {
		return "error"
	}
	return "(" + strings.Join(results, ",") + ",error)"
}

func (e *emitter) framedForeignCall(node syntax.Node, call, frame string, types []string) string {
	offset := 0
	if node != nil {
		offset = int(node.Pos().Offset())
	}
	temps := make([]string, len(types))
	for i := range temps {
		temps[i] = fmt.Sprintf("%sforeignResult%d_%d", e.prefix, offset, i)
	}
	var out strings.Builder
	if len(types) == 1 {
		fmt.Fprintf(&out, "func() %s {\n%sforeignStatus := %srt.Status\n%srt.Status = 0\n%s := %s\n", types[0], e.prefix, e.prefix, e.prefix, temps[0], call)
	} else {
		fmt.Fprintf(&out, "func() (%s) {\n%sforeignStatus := %srt.Status\n%srt.Status = 0\n%s := %s\n", strings.Join(types, ","), e.prefix, e.prefix, e.prefix, strings.Join(temps, ","), call)
	}
	fmt.Fprintf(&out, "if %srt.Status != 0 { return %s }\n%srt.Status = %sforeignStatus\n", e.prefix, strings.Join(temps, ","), e.prefix, e.prefix)
	for i, temp := range temps {
		fmt.Fprintf(&out, "%srt.MustResult(%srt.SetResult(%s,%d,%s))\n", e.prefix, e.prefix, frame, i, temp)
	}
	fmt.Fprintf(&out, "return %s\n}()", strings.Join(temps, ","))
	return out.String()
}

// framedForeignErrCall is the call site of foreignErrWrapper. Outside the
// execution runtime the adapter is called directly. Under it the call is
// framed like framedForeignCall: rt.Status is the adapter's scratch failure
// signal, so it is zeroed around the call and the outcome is carried into the
// program's status — 0 for success and for the function's own error, the
// failure status for a transport failure. Every result slot is recorded on
// every outcome, the zero results included, so the short declaration never
// reports a missing result for a call that did complete.
func (e *emitter) framedForeignErrCall(c *syntax.BashPPCall, foreign foreignFunction, frame string) (string, error) {
	values := make([]string, len(c.Args))
	for i := range c.Args {
		value, err := e.callArgument(c, i)
		if err != nil {
			return "", err
		}
		values[i] = value
	}
	call := e.foreignErrAdapterName(foreign.plan, foreign.export) + "(" + strings.Join(values, ",") + ")"
	if !e.execution {
		return call, nil
	}
	offset := int(c.Pos().Offset())
	resultTypes := make([]string, len(foreign.export.Signature.Results))
	temps := make([]string, len(resultTypes))
	for i, typ := range foreign.export.Signature.Results {
		resultTypes[i] = foreignGoType(typ)
		temps[i] = fmt.Sprintf("%sforeignResult%d_%d", e.prefix, offset, i)
	}
	returned := strings.Join(append(append([]string(nil), temps...), "err"), ",")
	var out strings.Builder
	fmt.Fprintf(&out, "func() %s {\n%sforeignStatus := %srt.Status\n%srt.Status = 0\n%s := %s\n", foreignErrSignature(resultTypes), e.prefix, e.prefix, e.prefix, returned, call)
	fmt.Fprintf(&out, "%sforeignExit := %srt.Status\n%srt.Status = %sforeignStatus\n%s.SetStatus(%sforeignExit)\n", e.prefix, e.prefix, e.prefix, e.prefix, e.program(), e.prefix)
	if frame != "" {
		for i, temp := range temps {
			fmt.Fprintf(&out, "%srt.MustResult(%srt.SetResult(%s,%d,%s))\n", e.prefix, e.prefix, frame, i, temp)
		}
		fmt.Fprintf(&out, "%srt.MustResult(%srt.SetResult(%s,%d,err))\n", e.prefix, e.prefix, frame, len(temps))
	}
	fmt.Fprintf(&out, "return %s\n}()", returned)
	return out.String(), nil
}

func foreignGoType(typ string) string {
	switch typ {
	case "bytes":
		return "[]byte"
	case "nil", "object", "handle", "callback":
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
