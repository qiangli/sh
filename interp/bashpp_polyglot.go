package interp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/polyglot"
	"mvdan.cc/sh/v3/syntax"
)

type bashPPForeignFunc struct {
	module    *polyglot.Module
	export    polyglot.Export
	qualified string
	direct    bool
	receiver  *polyglot.Handle
	argNames  []string
	argCells  []*bashPPCell
	call      *syntax.BashPPCall
}

func (r *Runner) bashPPPrepareSourceBlocks(ctx context.Context, file *syntax.File) (func(), error) {
	oldFuncs, oldModules, oldImports := r.bashPPForeignFuncs, r.bashPPForeignModules, r.bashPPForeignImports
	r.bashPPForeignFuncs = nil
	r.bashPPForeignModules = nil
	r.bashPPForeignImports = nil
	restore := func() {
		// Close only modules owned by this source unit. A nested `source` call
		// temporarily replaces the namespace but must leave its caller's
		// persistent workers alive when the nested file returns.
		for _, module := range r.bashPPForeignModules {
			_ = module.Close()
		}
		r.bashPPForeignFuncs, r.bashPPForeignModules, r.bashPPForeignImports = oldFuncs, oldModules, oldImports
	}
	if r.Dialect() != syntax.LangBashPP {
		return restore, nil
	}
	var blocks []polyglot.Block
	var imports []*syntax.BashPPImport
	for _, stmt := range file.Stmts {
		switch node := stmt.Cmd.(type) {
		case *syntax.SourceBlock:
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
		return restore, nil
	}
	source := file.Name
	if source == "" {
		source = filepath.Join(r.Dir, ".bashpp-stdin")
	} else if !filepath.IsAbs(source) {
		source = filepath.Join(r.Dir, source)
	}
	r.bashPPForeignImports = make(map[string]*polyglot.Module, len(imports))
	for _, imp := range imports {
		modulePath, err := strconv.Unquote(`"` + imp.Path.Parts[0].(*syntax.Lit).Value + `"`)
		if err != nil {
			return restore, err
		}
		alias, _ := syntax.BashPPDerivedImportAlias(modulePath)
		if imp.Alias != nil {
			alias = imp.Alias.Value
		}
		environment := ""
		if imp.Environment != nil {
			environment = imp.Environment.Value
		}
		plan, err := polyglot.PlanImport(polyglot.ImportRequest{Source: source, Language: imp.Language.Value, Environment: environment, Module: modulePath, Alias: alias, Environ: nativeExecEnv(execEnv(r.writeEnv))})
		if err != nil {
			return restore, fmt.Errorf("%s: %w", file.Name, err)
		}
		module := polyglot.StartImport(plan)
		r.bashPPForeignImports[alias] = module
		r.bashPPForeignModules = append(r.bashPPForeignModules, module)
	}
	if len(blocks) == 0 {
		r.bashPPForeignFuncs = map[string]*bashPPFunc{}
		return restore, nil
	}
	pythonRuntime := polyglot.Python{}
	typeScriptRuntime := polyglot.TypeScript{}
	rustRuntime := polyglot.Rust{}
	cRuntime := polyglot.C{}
	cppRuntime := polyglot.CPP{}
	goRuntime := polyglot.Go{}
	bashRuntime := ShellRuntime("bash", r.Dir, execEnv(r.writeEnv))
	shRuntime := ShellRuntime("sh", r.Dir, execEnv(r.writeEnv))
	for _, block := range blocks {
		language := polyglot.CanonicalLanguage(block.Language)
		if language == "python" || language == "typescript" || language == "rust" || language == "c" || language == "cpp" || language == "go" {
			source := file.Name
			if source == "" {
				source = filepath.Join(r.Dir, ".bashpp-stdin")
			} else if !filepath.IsAbs(source) {
				source = filepath.Join(r.Dir, source)
			}
			environment, err := polyglot.DiscoverEnvironment(polyglot.EnvironmentRequest{
				Source: source, Language: language, Environ: nativeExecEnv(execEnv(r.writeEnv)),
			})
			if err != nil {
				return restore, fmt.Errorf("%s: %w", file.Name, err)
			}
			if language == "python" {
				pythonRuntime.Environment = &environment
			} else if language == "typescript" {
				typeScriptRuntime.Environment = &environment
			} else if language == "rust" {
				rustRuntime.Environment = &environment
			} else if language == "c" {
				cRuntime.Environment = &environment
			} else if language == "cpp" {
				cppRuntime.Environment = &environment
			} else {
				goRuntime.Environment = &environment
			}
		}
	}
	plans, err := polyglot.Prepare(ctx, blocks, map[string]polyglot.Analyzer{
		"python": pythonRuntime, "typescript": typeScriptRuntime, "rust": rustRuntime, "c": cRuntime, "cpp": cppRuntime, "go": goRuntime,
		"bash": bashRuntime, "sh": shRuntime,
	})
	if err != nil {
		return restore, fmt.Errorf("%s: %w", file.Name, err)
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
			if n.Alias != nil {
				reserved[n.Alias.Value] = "import"
			}
		}
	}
	foreign := map[string]*bashPPFunc{}
	for _, plan := range plans {
		var runtime polyglot.Runtime = pythonRuntime
		if plan.Language == "typescript" {
			runtime = typeScriptRuntime
		} else if plan.Language == "rust" {
			runtime = rustRuntime
		} else if plan.Language == "c" {
			runtime = cRuntime
		} else if plan.Language == "cpp" {
			runtime = cppRuntime
		} else if plan.Language == "go" {
			runtime = goRuntime
		} else if plan.Language == "bash" {
			runtime = bashRuntime
		} else if plan.Language == "sh" {
			runtime = shRuntime
		}
		module := polyglot.Start(plan, runtime)
		r.bashPPForeignModules = append(r.bashPPForeignModules, module)
		if plan.Alias != "" {
			if kind := reserved[plan.Alias]; kind != "" {
				restore()
				return func() {}, fmt.Errorf("polyglot alias %s collides with %s", plan.Alias, kind)
			}
			reserved[plan.Alias] = "foreign module"
		}
		for _, export := range plan.Exports {
			name := export.Name
			if plan.Alias != "" {
				name = plan.Alias + "." + name
			}
			if kind := reserved[name]; kind != "" {
				restore()
				return func() {}, fmt.Errorf("polyglot callable %s collides with %s", name, kind)
			}
			if foreign[name] != nil {
				restore()
				return func() {}, fmt.Errorf("polyglot callable %s is exported more than once", name)
			}
			decl := foreignDecl(name, export.Signature)
			foreign[name] = &bashPPFunc{decl: decl, foreign: &bashPPForeignFunc{module: module, export: export, qualified: name}}
			reserved[name] = "foreign callable"
		}
	}
	r.bashPPForeignFuncs = foreign
	return restore, nil
}

func foreignDecl(name string, sig polyglot.Signature) *syntax.BashPPFuncDecl {
	base := name
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		base = name[i+1:]
	}
	decl := &syntax.BashPPFuncDecl{Name: &syntax.Lit{Value: base}}
	if sig.Dynamic {
		decl.Params = []*syntax.BashPPField{{Names: []*syntax.Lit{{Value: "args"}}, FieldType: &syntax.Lit{Value: "any"}, Ellipsis: syntax.NewPos(0, 1, 1)}}
		decl.Results = []*syntax.BashPPField{{FieldType: &syntax.Lit{Value: "any"}}, {FieldType: &syntax.Lit{Value: "error"}}}
		return decl
	}
	for i, typ := range sig.Params {
		field := &syntax.BashPPField{Names: []*syntax.Lit{{Value: fmt.Sprintf("arg%d", i)}}, FieldType: &syntax.Lit{Value: foreignShellType(typ)}}
		if sig.Variadic && i == len(sig.Params)-1 {
			field.Ellipsis = syntax.NewPos(0, 1, 1)
		}
		decl.Params = append(decl.Params, field)
	}
	for _, typ := range sig.Results {
		decl.Results = append(decl.Results, &syntax.BashPPField{FieldType: &syntax.Lit{Value: foreignShellType(typ)}})
	}
	return decl
}

func foreignShellType(typ string) string {
	if typ == "object" {
		return "any"
	}
	return typ
}

func (r *Runner) bashPPInvokeForeign(ctx context.Context, fn *bashPPForeignFunc, args []string) []string {
	if fn.direct {
		values := make([]any, len(args))
		for i, arg := range args {
			values[i] = arg
			if i < len(fn.argCells) && fn.argCells[i] != nil && fn.argCells[i].vr.Kind == expand.Object {
				values[i] = fn.argCells[i].vr.Obj
			} else if fn.call != nil && i < len(fn.call.ArgExprs) {
				switch expr := fn.call.ArgExprs[i].(type) {
				case *syntax.BashPPBasicLit:
					switch expr.Kind {
					case "INT":
						values[i], _ = strconv.ParseInt(arg, 0, 64)
					case "FLOAT":
						values[i], _ = strconv.ParseFloat(arg, 64)
					case "STRING":
						values[i] = strings.Trim(arg, `"'`)
					}
				case *syntax.BashPPIdent:
					if expr.Name.Value == "true" {
						values[i] = true
					}
					if expr.Name.Value == "false" {
						values[i] = false
					}
				}
			} else if fn.call != nil && i < len(fn.call.Args) {
				if lit := fn.call.Args[i].Lit(); lit != "" {
					if integer, err := strconv.ParseInt(lit, 0, 64); err == nil {
						values[i] = integer
					} else if number, err := strconv.ParseFloat(lit, 64); err == nil {
						values[i] = number
					} else if lit == "true" || lit == "false" {
						values[i] = lit == "true"
					}
				}
			}
		}
		positional := len(values) - len(fn.argNames)
		kwargs := make(map[string]any, len(fn.argNames))
		for i, name := range fn.argNames {
			kwargs[name] = values[positional+i]
		}
		values = values[:positional]
		var result polyglot.CallResult
		var err error
		if fn.receiver != nil {
			if fn.export.Name == "" {
				result, err = fn.receiver.Call(ctx, values, kwargs)
			} else {
				result, err = fn.receiver.CallAttr(ctx, fn.export.Name, values, kwargs)
			}
		} else {
			result, err = fn.module.CallKeywords(ctx, fn.export.Name, values, kwargs)
		}
		if result.Stdout != "" {
			fmt.Fprint(r.stdout, result.Stdout)
		}
		if result.Stderr != "" {
			fmt.Fprint(r.stderr, result.Stderr)
		}
		if err != nil {
			r.errf("bash++: foreign call %s failed: %v\n", fn.qualified, err)
			r.exit.code = 1
			return nil
		}
		r.exit = exitStatus{}
		if handle, ok := result.Value.(*polyglot.Handle); ok {
			r.bashPPResultCells = []*bashPPCell{{vr: expand.NewObject(handle)}}
			return []string{""}
		}
		if len(fn.export.Signature.Results) == 1 && fn.export.Signature.Results[0] == "object" {
			r.bashPPResultCells = []*bashPPCell{{vr: expand.NewObject(result.Value)}}
		}
		return []string{foreignResult(result.Value)}
	}
	values := make([]any, len(args))
	for i, arg := range args {
		typ := "any"
		if !fn.export.Signature.Dynamic && len(fn.export.Signature.Params) > 0 {
			if i < len(fn.export.Signature.Params) {
				typ = fn.export.Signature.Params[i]
			} else if fn.export.Signature.Variadic {
				typ = fn.export.Signature.Params[len(fn.export.Signature.Params)-1]
			}
		}
		value, err := foreignArgument(arg, typ)
		if err != nil {
			r.errf("bash++: %s argument %d: %v\n", fn.qualified, i+1, err)
			r.exit.code = 2
			return nil
		}
		values[i] = value
	}
	result, err := fn.module.Call(ctx, fn.export.Name, values...)
	if result.Stdout != "" {
		fmt.Fprint(r.stdout, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprint(r.stderr, result.Stderr)
	}
	if err != nil {
		if fn.export.Signature.Dynamic {
			r.exit = exitStatus{}
			return []string{"", err.Error()}
		}
		r.errf("bash++: foreign call %s failed: %v\n", fn.qualified, err)
		r.exit.code = 1
		return nil
	}
	r.exit = exitStatus{}
	value := foreignResult(result.Value)
	if err != nil && len(fn.export.Signature.Results) > 0 {
		value = foreignZero(fn.export.Signature.Results[0])
	}
	if len(fn.export.Signature.Results) == 1 && fn.export.Signature.Results[0] == "object" {
		r.bashPPResultCells = []*bashPPCell{{vr: expand.NewObject(result.Value)}}
	}
	if fn.export.Signature.Dynamic {
		return []string{value, ""}
	}
	if len(fn.export.Signature.Results) == 0 {
		return nil
	}
	return []string{value}
}

func (r *Runner) bashPPInvokeForeignErr(ctx context.Context, fn *bashPPForeignFunc, args []string) []string {
	var result polyglot.CallResult
	var err error
	if fn.direct {
		values := make([]any, len(args))
		for i, arg := range args {
			values[i] = arg
			if i < len(fn.argCells) && fn.argCells[i] != nil && fn.argCells[i].vr.Kind == expand.Object {
				values[i] = fn.argCells[i].vr.Obj
			}
		}
		positional := len(values) - len(fn.argNames)
		kwargs := make(map[string]any, len(fn.argNames))
		for i, name := range fn.argNames {
			kwargs[name] = values[positional+i]
		}
		values = values[:positional]
		if fn.receiver != nil {
			if fn.export.Name == "" {
				result, err = fn.receiver.Call(ctx, values, kwargs)
			} else {
				result, err = fn.receiver.CallAttr(ctx, fn.export.Name, values, kwargs)
			}
		} else {
			result, err = fn.module.CallKeywords(ctx, fn.export.Name, values, kwargs)
		}
	} else {
		values := make([]any, len(args))
		for i, arg := range args {
			typ := "any"
			if len(fn.export.Signature.Params) > 0 {
				if i < len(fn.export.Signature.Params) {
					typ = fn.export.Signature.Params[i]
				} else if fn.export.Signature.Variadic {
					typ = fn.export.Signature.Params[len(fn.export.Signature.Params)-1]
				}
			}
			value, convErr := foreignArgument(arg, typ)
			if convErr != nil {
				r.errf("bash++: %s argument %d: %v\n", fn.qualified, i+1, convErr)
				r.exit.code = 2
				return nil
			}
			values[i] = value
		}
		result, err = fn.module.Call(ctx, fn.export.Name, values...)
	}
	if result.Stdout != "" {
		fmt.Fprint(r.stdout, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprint(r.stderr, result.Stderr)
	}
	r.exit = exitStatus{}
	value := foreignResult(result.Value)
	if err != nil && len(fn.export.Signature.Results) > 0 {
		value = foreignZero(fn.export.Signature.Results[0])
	}
	cells := []*bashPPCell{{vr: expand.Variable{Set: true, Kind: expand.String, Str: value}}, bashPPForeignErrorCell(err)}
	if err == nil && len(fn.export.Signature.Results) == 1 && fn.export.Signature.Results[0] == "object" {
		cells[0] = &bashPPCell{vr: expand.NewObject(result.Value)}
	}
	r.bashPPResultCells = cells
	if err != nil {
		return []string{value, err.Error()}
	}
	return []string{value, ""}
}

func bashPPForeignErrorCell(err error) *bashPPCell {
	errType := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "error"}}
	if err == nil {
		return &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String}, declType: errType, interfaceValue: &bashPPInterfaceValue{nilIface: true}}
	}
	payload := &bashPPCell{vr: expand.NewObject(err), declType: errType}
	return &bashPPCell{
		vr:             expand.Variable{Set: true, Kind: expand.String, Str: err.Error()},
		declType:       errType,
		interfaceValue: &bashPPInterfaceValue{cell: payload, dynamic: errType},
	}
}

func foreignArgument(text, typ string) (any, error) {
	switch typ {
	case "int":
		return strconv.ParseInt(text, 0, 64)
	case "float64":
		return strconv.ParseFloat(text, 64)
	case "bool":
		return strconv.ParseBool(text)
	case "bytes":
		return []byte(text), nil
	case "string", "any":
		return text, nil
	case "object":
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("want JSON object: %w", err)
		}
		return normalizeForeignJSON(value)
	case "nil":
		if text == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("want nil")
	default:
		return text, nil
	}
}

func foreignZero(typ string) string {
	switch typ {
	case "bool":
		return "false"
	case "float64", "int":
		return "0"
	}
	return ""
}

func normalizeForeignJSON(value any) (any, error) {
	switch value := value.(type) {
	case json.Number:
		if integer, err := value.Int64(); err == nil {
			return integer, nil
		}
		return value.Float64()
	case []any:
		for i := range value {
			var err error
			value[i], err = normalizeForeignJSON(value[i])
			if err != nil {
				return nil, err
			}
		}
	case map[string]any:
		for key := range value {
			var err error
			value[key], err = normalizeForeignJSON(value[key])
			if err != nil {
				return nil, err
			}
		}
	}
	return value, nil
}

func foreignResult(value any) string {
	switch x := value.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		data, _ := json.Marshal(x)
		return string(data)
	}
}
