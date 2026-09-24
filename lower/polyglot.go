package lower

import (
	"bytes"
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

// loweredRuntimeImports lists the import paths the fence runtimes of this
// unit need in the lowered program, as their rows declare them, plus the
// interpreter when shell regions must see the unit's direct imports.
func (e *emitter) loweredRuntimeImports() []string {
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	for _, plan := range e.foreignPlans {
		if plan.Runner != "" {
			// The runner adapter trims the captured stdout as the
			// interpreter does; the runner's own row, if any, is bypassed.
			add("strings")
			continue
		}
		row, _ := polyglot.LookupLanguage(plan.Language)
		for _, path := range row.LoweredImports {
			add(path)
		}
	}
	if e.seedsForeignImports() {
		add("mvdan.cc/sh/v3/interp")
	}
	sort.Strings(paths)
	return paths
}

// seedsForeignImports reports whether the program's shell regions must see
// its direct Python imports: a mixed unit hands them to the region backend
// through interp.ForeignImports so `alias.fn args` in a region reaches the
// program's own worker (B8), not an external lookup.
// hasForeign reports whether the unit carries foreign fences or imports.
func (e *emitter) hasForeign() bool {
	return len(e.foreignPlans) > 0 || len(e.foreignImports) > 0
}

func (e *emitter) seedsForeignImports() bool {
	return e.mixedShell && (len(e.foreignImports) > 0 || e.hasForeignStreams())
}

func (e *emitter) hasForeignStreams() bool {
	for _, f := range e.foreignFunctions {
		if f.export.Signature.Iterator != "" {
			return true
		}
	}
	return false
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
	for i, plan := range e.foreignPlans {
		if plan.Alias == "" {
			continue
		}
		if len(aliases) > 0 {
			out.WriteString(",")
		}
		fmt.Fprintf(&out, "%s:%sforeign%d", strconv.Quote(plan.Alias), e.prefix, i)
		aliases = append(aliases, plan.Alias)
	}
	out.WriteString("}))")
	return out.String()
}

func (e *emitter) prepareForeign(ctx context.Context, file *syntax.File) error {
	var blocks []polyglot.Block
	var first *syntax.SourceBlock
	var imports []*syntax.BashPPImport
	var runnerBlocks map[string]*syntax.SourceBlock
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
			runner := ""
			if node.Runner != nil {
				// A runner fence lowers when its runner is a Bash# function
				// of the unit (runnerLowers); the block is kept so the
				// analyzer can be the runner itself, whatever the type's row.
				runner = node.Runner.Value
				if runnerBlocks == nil {
					runnerBlocks = map[string]*syntax.SourceBlock{}
				}
				runnerBlocks[polyglot.CanonicalLanguage(node.Language.Value)] = node
			}
			blocks = append(blocks, polyglot.Block{Language: node.Language.Value, Alias: alias, Runner: runner, Source: node.Body, Filename: file.Name, Line: int(node.BodyPos.Line())})
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
	// One runtime per language per unit, from its row; the environment is
	// kept so the lowered program can construct the same runtime.
	e.foreignEnvs = map[string]*polyglot.EnvironmentPlan{}
	analyzers := map[string]polyglot.Analyzer{}
	manifests, err := polyglot.ManifestFiles(blocks)
	if err != nil {
		return e.fail(first, CodeUnsupported, err.Error())
	}
	var runnerStderr bytes.Buffer
	for _, block := range blocks {
		language := polyglot.CanonicalLanguage(block.Language)
		if _, done := analyzers[language]; done {
			continue
		}
		// A runner override processes the body whatever the type, as in the
		// interpreter; its methods are answered at compile time by the
		// runner's own declaration, evaluated through the interpreter.
		if node := runnerBlocks[language]; node != nil {
			if block.Alias == "" {
				return e.fail(node, CodeType, "runner fence "+block.Language+" needs an alias (as NAME)")
			}
			if err := e.runnerLowers(file, node); err != nil {
				return err
			}
			analyzers[language] = polyglot.RunnerFence{Type: language, Runner: node.Runner.Value, Invoke: func(ctx context.Context, argv []string) (string, error) {
				return interp.FenceRunnerInvoke(ctx, file, node, e.options.Dir, &runnerStderr, argv)
			}}
			continue
		}
		row, ok := polyglot.LookupLanguage(language)
		if !ok {
			continue
		}
		config := polyglot.RuntimeConfig{Dir: e.options.Dir, Environ: os.Environ()}
		if row.NeedsEnvironment {
			environment, err := polyglot.DiscoverEnvironment(polyglot.EnvironmentRequest{Source: source, Language: language, ModuleFile: manifests[language]})
			if err != nil {
				return e.fail(first, CodeUnsupported, err.Error())
			}
			e.foreignEnvs[language] = &environment
			config.Environment = &environment
		}
		if row.Text && block.Alias == "" {
			return e.fail(first, CodeType, "text fence "+block.Language+" needs an alias (as NAME)")
		}
		if row.InterpretedOnly {
			// Its processor is the shell that would run the program — a dag
			// runner, a skills ring — and a lowered program carries no
			// shell to hand the body to: a transpiled binary never depends
			// on a bashy on the target, so the row stays interpreted.
			return e.fail(first, CodeUnsupported, "text fence ~~~"+block.Language+": its processor is the running shell, which a lowered program does not carry; run it interpreted, or process the body with a Bash# function runner (~~~"+block.Language+" as NAME !func), which lowers")
		}
		analyzers[language] = row.NewRuntime(config)
	}
	var plans []polyglot.Plan
	if len(blocks) > 0 {
		var err error
		plans, err = polyglot.Prepare(ctx, blocks, analyzers)
		if err != nil {
			text := err.Error()
			if diagnostics := strings.TrimSpace(runnerStderr.String()); diagnostics != "" {
				text += ": " + diagnostics
			}
			return e.fail(first, CodeUnsupported, text)
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
	// Callback invocation needs an explicit Program context, including for
	// otherwise scalar-only units. The hidden callable ABI supplies that
	// context without process-global or goroutine-local re-entry authority.
	for _, plan := range plans {
		if row, _ := polyglot.LookupLanguage(plan.Language); !row.Callbacks {
			continue
		}
		for _, export := range plan.Exports {
			for _, typ := range export.Signature.Params {
				if typ == "callback" {
					e.execution = true
				}
			}
		}
	}
	if len(plans) > 0 || len(imports) > 0 {
		e.bridge = true
		e.output = true
	}
	return nil
}

func (e *emitter) findPythonValues(file *syntax.File) {
	e.foreignIteratorValues = map[string]string{}
	for range 3 {
		syntax.Walk(file, func(node syntax.Node) bool {
			d, ok := node.(*syntax.BashPPShortDecl)
			if !ok || len(d.Lhs) != 1 {
				return true
			}
			if d.Call != nil && e.pythonCallKind(d.Call) != "" || d.Expr != nil && e.pythonExpr(d.Expr) {
				e.pythonValues[d.Lhs[0].Value] = true
			}
			if d.Call != nil {
				if f, ok := e.foreignFunctions[strings.Join(names(d.Call.Fun), ".")]; ok && f.export.Signature.Iterator != "" {
					// Binding ownership is recorded during lexical emission, not by name here.
					e.mixedShell = true
				}
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
	fmt.Fprintf(&out, "var _ = %scontext.Background\n", e.prefix)
	if e.execution && len(e.foreignPlans) > 0 {
		// Adapter plumbing is not a script callable scope: the private alias
		// keeps lexical-storage registration out of generated adapter locals.
		fmt.Fprintf(&out, "type %sforeignProgramState = %srt.Program\n", e.prefix, e.prefix)
	}
	for i, plan := range e.foreignImports {
		e.foreignGlobals[fmt.Sprintf("%spython%d", e.prefix, i)] = true
		fmt.Fprintf(&out, "var %spython%d = %spolyglot.StartImport(%spolyglot.ImportPlan{ID:%s,Language:%s,Module:%s,Alias:%s,Path:%s,Environment:*%s})\n", e.prefix, i, e.prefix, e.prefix, strconv.Quote(plan.ID), strconv.Quote(plan.Language), strconv.Quote(plan.Module), strconv.Quote(plan.Alias), strconv.Quote(plan.Path), e.environmentLiteral(&plan.Environment))
	}
	if len(e.foreignImports) > 0 {
		fmt.Fprintf(&out, "var _ = %scontext.Background\n", e.prefix)
		fmt.Fprintf(&out, "func %spythonValue(result %spolyglot.CallResult, err error) any { if result.Stdout != \"\" { %sfmt.Fprint(%srt.Stdout,result.Stdout) }; if result.Stderr != \"\" { %sfmt.Fprint(%srt.Stderr,result.Stderr) }; if err != nil { panic(%srt.ValueAbort{Err:err}) }; return result.Value }\n", e.prefix, e.prefix, e.prefix, e.prefix, e.prefix, e.prefix, e.prefix)
		fmt.Fprintf(&out, "func %spythonHandle(value any) *%spolyglot.Handle { handle,ok:=value.(*%spolyglot.Handle); if !ok { panic(%srt.ValueAbort{Err:%sfmt.Errorf(\"Python value %%T is not an object\",value)}) }; return handle }\n", e.prefix, e.prefix, e.prefix, e.prefix, e.prefix)
	}
	for i, plan := range e.foreignPlans {
		module := fmt.Sprintf("%sforeign%d", e.prefix, i)
		e.foreignGlobals[module] = true
		row, _ := polyglot.LookupLanguage(plan.Language)
		runtime := ""
		if plan.Runner != "" {
			runtime = e.runnerLiteral(i, plan)
			out.WriteString(e.runnerAdapter(i, plan))
		} else {
			runtime = row.LoweredRuntime(e.prefix, e.environmentLiteral(e.foreignEnvs[plan.Language]))
		}
		fmt.Fprintf(&out, "var %s = %spolyglot.Start(%spolyglot.Plan{ID:%s,Language:%s,Alias:%s,Runner:%s,Source:%s,Artifact:%s,Exports:%s}, %s)\n", module, e.prefix, e.prefix, strconv.Quote(plan.ID), strconv.Quote(plan.Language), strconv.Quote(plan.Alias), strconv.Quote(plan.Runner), strconv.Quote(plan.Source), strconv.Quote(plan.Artifact), e.foreignExports(plan.Exports), runtime)
		if row.Callbacks && plan.Runner == "" {
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
				out.WriteString(e.foreignWrapper("("+alias+" "+typ+") ", module, export, plan.Runner != ""))
				out.WriteString(e.foreignErrWrapper(i, module, plan.Alias, export, plan.Runner != ""))
			}
		} else {
			for _, export := range plan.Exports {
				out.WriteString(e.foreignWrapper("", module, export, plan.Runner != ""))
				out.WriteString(e.foreignErrWrapper(i, module, plan.Alias, export, plan.Runner != ""))
			}
		}
	}
	return out.String()
}

// foreignCallbacks captures the calling Program with each callback value.
// Nested calls receive a copy with the callback's re-entry context; unrelated
// goroutines never observe or inherit that authority.
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
	if !e.execution {
		return ""
	}
	fmt.Fprintf(&out, "func %s(%sprogram *%srt.Program, %starget any) %spolyglot.Callback {\n", resolver, e.prefix, e.prefix, e.prefix, e.prefix)
	out.WriteString(strings.ReplaceAll(`PREFIXname := "closure"
if PREFIXnamed, PREFIXok := PREFIXtarget.(string); PREFIXok {
PREFIXname = PREFIXnamed
switch PREFIXname {
`, "PREFIX", e.prefix))
	for _, name := range names {
		fmt.Fprintf(&out, "case %s:\n%starget = %s\n", strconv.Quote(name), e.prefix, e.goName(name))
	}
	out.WriteString(strings.ReplaceAll(`default:
return PREFIXpolyglot.Callback{Name:PREFIXname, Invoke:func(PREFIXcontext.Context,[]any)(any,error){return nil,PREFIXfmt.Errorf("unknown shell callback %q",PREFIXname)}}
} }
PREFIXcallback := PREFIXpolyglot.FuncCallback(PREFIXname,PREFIXtarget)
return PREFIXpolyglot.Callback{Name:PREFIXname, Invoke:func(PREFIXctx PREFIXcontext.Context,PREFIXargs []any)(any,error){
PREFIXchild := *PREFIXprogram
PREFIXchild.Context = PREFIXctx
return PREFIXcallback.Invoke(PREFIXctx,append([]any{&PREFIXchild,PREFIXrt.Site{Name:PREFIXname}},PREFIXargs...))
}}
}
`, "PREFIX", e.prefix))
	fmt.Fprintf(&out, "func init() {\n%s.SetCallbacks(%spolyglot.Callbacks{\n", module, e.prefix)
	fmt.Fprintf(&out, "Output: func(stdout, stderr string) { if stdout != \"\" { %sfmt.Fprint(%srt.Stdout, stdout) }; if stderr != \"\" { %sfmt.Fprint(%srt.Stderr, stderr) } },\n})\n}\n", e.prefix, e.prefix, e.prefix, e.prefix)
	return out.String()
}

func (e *emitter) environmentLiteral(p *polyglot.EnvironmentPlan) string {
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("&%spolyglot.EnvironmentPlan{Language:%s,Name:%s,Root:%s,Dir:%s,SourceDir:%s,Executable:%s,Manager:%s,RuntimeConstraint:%s,Runtime:%s,CompilerModule:%s,Manifests:%#v,Locks:%#v,PythonPath:%#v,Env:%#v,Explanation:%#v,ResolutionFiles:%#v,Fingerprint:%s,ModuleFile:%s}",
		e.prefix, strconv.Quote(p.Language), strconv.Quote(p.Name), strconv.Quote(p.Root), strconv.Quote(p.Dir), strconv.Quote(p.SourceDir), strconv.Quote(p.Executable), strconv.Quote(p.Manager), strconv.Quote(p.RuntimeConstraint), strconv.Quote(p.Runtime), strconv.Quote(p.CompilerModule), p.Manifests, p.Locks, p.PythonPath, p.Env, p.Explanation, p.ResolutionFiles, strconv.Quote(p.Fingerprint), strconv.Quote(p.ModuleFile))
}

func (e *emitter) foreignExports(exports []polyglot.Export) string {
	var out strings.Builder
	fmt.Fprintf(&out, "[]%spolyglot.Export{", e.prefix)
	for _, export := range exports {
		fmt.Fprintf(&out, "{Name:%s,Signature:%spolyglot.Signature{Params:%#v,Results:%#v,Dynamic:%t,Variadic:%t,Iterator:%q,Filter:%t},Effects:%#v},", strconv.Quote(export.Name), e.prefix, export.Signature.Params, export.Signature.Results, export.Signature.Dynamic, export.Signature.Variadic, export.Signature.Iterator, export.Signature.Filter, export.Effects)
	}
	out.WriteByte('}')
	return out.String()
}

func (e *emitter) foreignWrapper(receiver, module string, export polyglot.Export, runner bool) (source string) {
	defer func() { source = e.foreignProgramStatus(source, e.prefix+"foreignProgram") }()
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
	ctx := e.prefix + "context.Background()"
	if e.execution {
		params = append([]string{e.prefix + "foreignProgram *" + e.prefix + "foreignProgramState"}, params...)
		ctx = e.prefix + "foreignProgram.Context"
		if runner {
			// The runner's Invoke calls the lowered function on the
			// Program making this call (runnerAdapter).
			ctx = e.prefix + "polyglot.WithHost(" + ctx + ", " + e.prefix + "foreignProgram)"
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "func %s%s(%s)", receiver, export.Name, strings.Join(params, ","))
	if len(results) == 1 {
		fmt.Fprintf(&out, " %s", results[0])
	} else if len(results) > 1 {
		fmt.Fprintf(&out, " (%s)", strings.Join(results, ","))
	}
	out.WriteString(" {\n")
	if export.Signature.Iterator != "" {
		values := strings.TrimPrefix(args, ",")
		fmt.Fprintf(&out, "return %sshellexec.NewForeignIterator(%s,%q,[]any{%s})\n}\n", e.prefix, module, export.Name, values)
		return out.String()
	}
	fmt.Fprintf(&out, "result, err := %s.Call(%s, %s%s)\n", module, ctx, strconv.Quote(export.Name), args)
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
func (e *emitter) foreignErrWrapper(plan int, module, alias string, export polyglot.Export, runner bool) (source string) {
	defer func() { source = e.foreignProgramStatus(source, e.prefix+"foreignProgram") }()
	if export.Signature.Dynamic || export.Signature.Iterator != "" {
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
	ctx := e.prefix + "context.Background()"
	if e.execution {
		params = append([]string{e.prefix + "foreignProgram *" + e.prefix + "foreignProgramState"}, params...)
		ctx = e.prefix + "foreignProgram.Context"
		if runner {
			// The runner's Invoke calls the lowered function on the
			// Program making this call (runnerAdapter).
			ctx = e.prefix + "polyglot.WithHost(" + ctx + ", " + e.prefix + "foreignProgram)"
		}
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
	fmt.Fprintf(&out, "result, err := %s.Call(%s, %s%s)\n", module, ctx, strconv.Quote(export.Name), args)
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

// The legacy adapter spells its status using the package runtime. Execution
// units instead own status in their Program, just as they own their context.
// Keep the common adapter emission but redirect its four status operations;
// no mutable scratch state can be shared by independent entry invocations.
func (e *emitter) foreignProgramStatus(source, program string) string {
	if !e.execution {
		return source
	}
	p := e.prefix
	return strings.NewReplacer(
		p+"rt.Status = "+p+"rt.ExitCode(err)", program+".SetStatus("+p+"rt.ExitCode(err))",
		p+"rt.Status = "+p+"foreignStatus", program+".SetStatus("+p+"foreignStatus)",
		p+"rt.Status = 0", program+".SetStatus(0)",
		p+"rt.Status", program+".Status()",
	).Replace(source)
}

func (e *emitter) framedForeignCall(node syntax.Node, call, frame string, types []string) (source string) {
	defer func() { source = e.foreignProgramStatus(source, e.program()) }()
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
func (e *emitter) framedForeignErrCall(c *syntax.BashPPCall, foreign foreignFunction, frame string) (source string, problem error) {
	defer func() { source = e.foreignProgramStatus(source, e.program()) }()
	values := make([]string, len(c.Args))
	for i := range c.Args {
		value, err := e.callArgument(c, i)
		if err != nil {
			return "", err
		}
		values[i] = value
	}
	if e.execution {
		values = append([]string{e.program()}, values...)
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
	case "textio":
		return "any"
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

// runnerContract is the signature a fence runner declares, verbatim from the
// text-fence plan: `runner <verb> <file> [args…]`, stdout or the returned
// string is the value.
const runnerContract = "func NAME(verb string, file string, args ...string) string"

// runnerLowers decides whether the fence's runner lowers with the program:
// only a Bash# function of the unit does, with the runner contract's
// signature, since that is what the emitter can call directly. A shell
// function lives in the interpreter session, a builtin and a registered
// command in the shell that runs the program — none of which a transpiled
// binary carries — so those are refused by name, with the route: a Bash#
// function runner wrapping the command.
func (e *emitter) runnerLowers(file *syntax.File, block *syntax.SourceBlock) error {
	runner := block.Runner.Value
	fence := "runner fence ~~~" + block.Language.Value + " !" + runner
	for _, stmt := range file.Stmts {
		switch decl := stmt.Cmd.(type) {
		case *syntax.FuncDecl:
			if decl.Name.Value == runner {
				return e.fail(block, CodeUnsupported, fence+": "+runner+" is a shell function, which runs in the interpreter session a lowered program does not carry; declare the runner as a Bash# function ("+strings.ReplaceAll(runnerContract, "NAME", runner)+") to lower it")
			}
		case *syntax.BashPPFuncDecl:
			if decl.Receiver != nil || decl.Name.Value != runner {
				continue
			}
			if !runnerSignatureMatches(decl) {
				return e.fail(decl, CodeType, fence+": "+runner+" does not have the runner signature "+strings.ReplaceAll(runnerContract, "NAME", runner))
			}
			return nil
		}
	}
	return e.fail(block, CodeUnsupported, fence+": "+runner+" is not a Bash# function of this unit (a builtin or a registered command runs in the shell that runs the program, which a lowered program does not carry); wrap it in a Bash# function ("+strings.ReplaceAll(runnerContract, "NAME", runner)+") to lower it")
}

// runnerSignatureMatches reports whether the declaration spells the runner
// contract: two string parameters, a variadic string tail, one string
// result, no type parameters and no defaults.
func runnerSignatureMatches(decl *syntax.BashPPFuncDecl) bool {
	if len(decl.TypeParams) > 0 {
		return false
	}
	var params []*syntax.BashPPField
	for _, f := range decl.Params {
		if f.Default != nil || f.FieldType == nil || f.FieldType.Value != "string" {
			return false
		}
		if f.Variadic() {
			params = append(params, f)
			continue
		}
		for range f.Names {
			params = append(params, f)
		}
	}
	if len(params) != 3 || params[0].Variadic() || params[1].Variadic() || !params[2].Variadic() {
		return false
	}
	if len(decl.Results) != 1 || decl.Results[0].FieldType == nil || decl.Results[0].FieldType.Value != "string" || len(decl.Results[0].Names) > 1 {
		return false
	}
	return true
}

// runnerLiteral is the lowered runtime of a runner plan: a RunnerFence whose
// Invoke is the adapter runnerAdapter emits.
func (e *emitter) runnerLiteral(plan int, p polyglot.Plan) string {
	return fmt.Sprintf("%spolyglot.RunnerFence{Type:%s,Runner:%s,Invoke:%sforeignRunner%d}", e.prefix, strconv.Quote(p.Language), strconv.Quote(p.Runner), e.prefix, plan)
}

// runnerAdapter emits the Invoke of a runner plan: a direct call of the
// lowered runner function with `<verb> <file> [args…]`, its stdout captured
// the way the interpreter captures a Bash# function runner's — a returned
// string is the value and what was printed reaches stdout; a function that
// printed instead has its stdout, minus trailing newlines, as the value.
// Under the execution runtime the call needs the Program making it, which
// the wrapper attaches to the context (polyglot.WithHost), and the capture
// swaps the session's stdout so shell regions of the body are caught too.
func (e *emitter) runnerAdapter(plan int, p polyglot.Plan) string {
	pre := e.prefix
	name := e.goName(p.Runner)
	runner := strconv.Quote(p.Runner)
	var out strings.Builder
	fmt.Fprintf(&out, "func %sforeignRunner%d(%sctx %scontext.Context, %sargv []string) (string, error) {\n", pre, plan, pre, pre, pre)
	fmt.Fprintf(&out, "if len(%sargv) < 2 { return \"\", %sfmt.Errorf(\"runner %%s: verb and file expected\", %s) }\n", pre, pre, runner)
	fmt.Fprintf(&out, "var %sout %sstrings.Builder\n", pre, pre)
	if e.execution {
		fmt.Fprintf(&out, "%sprogram, _ := %spolyglot.HostFrom(%sctx).(*%srt.Program)\n", pre, pre, pre, pre)
		fmt.Fprintf(&out, "if %sprogram == nil || %sprogram.Session == nil { return \"\", %sfmt.Errorf(\"runner %%s: no program to run it\", %s) }\n", pre, pre, pre, runner)
		fmt.Fprintf(&out, "%ssaved := %sprogram.Session.Stdio().Out\n", pre, pre)
		fmt.Fprintf(&out, "%sprogram.Session.SetStdio(nil, &%sout, nil)\n", pre, pre)
		fmt.Fprintf(&out, "%svalue := %s(%sprogram, %srt.Site{Name:%s}, %sargv[0], %sargv[1], %sargv[2:]...)\n", pre, name, pre, pre, runner, pre, pre, pre)
		fmt.Fprintf(&out, "%sprogram.Session.SetStdio(nil, %ssaved, nil)\n", pre, pre)
	} else {
		fmt.Fprintf(&out, "%ssaved := %srt.Stdout\n", pre, pre)
		fmt.Fprintf(&out, "%srt.Stdout = &%sout\n", pre, pre)
		fmt.Fprintf(&out, "%svalue := %s(%sargv[0], %sargv[1], %sargv[2:]...)\n", pre, name, pre, pre, pre)
		fmt.Fprintf(&out, "%srt.Stdout = %ssaved\n", pre, pre)
	}
	fmt.Fprintf(&out, "if %svalue != \"\" { %sfmt.Fprint(%ssaved, %sout.String()); return %svalue, nil }\n", pre, pre, pre, pre, pre)
	fmt.Fprintf(&out, "return %sstrings.TrimRight(%sout.String(), \"\\n\"), nil\n}\n", pre, pre)
	return out.String()
}
