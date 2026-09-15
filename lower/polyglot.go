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
	for _, stmt := range file.Stmts {
		block, ok := stmt.Cmd.(*syntax.SourceBlock)
		if !ok {
			continue
		}
		if first == nil {
			first = block
		}
		alias := ""
		if block.Alias != nil {
			alias = block.Alias.Value
		}
		blocks = append(blocks, polyglot.Block{Language: block.Language.Value, Alias: alias, Source: block.Body, Filename: file.Name, Line: int(block.BodyPos.Line())})
	}
	if len(blocks) == 0 {
		return nil
	}
	pythonRuntime := polyglot.Python{}
	for _, block := range blocks {
		if strings.EqualFold(strings.TrimSpace(block.Language), "python") {
			source := e.sourceName
			if source == "" {
				source = filepath.Join(e.options.Dir, ".bashpp-input")
			} else if !filepath.IsAbs(source) && e.options.Dir != "" {
				source = filepath.Join(e.options.Dir, source)
			}
			environment, err := polyglot.DiscoverEnvironment(polyglot.EnvironmentRequest{Source: source, Language: "python"})
			if err != nil {
				return e.fail(first, CodeUnsupported, err.Error())
			}
			e.foreignPythonEnv = &environment
			pythonRuntime.Environment = &environment
			break
		}
	}
	plans, err := polyglot.Prepare(ctx, blocks, map[string]polyglot.Analyzer{
		"python": pythonRuntime, "typescript": polyglot.TypeScript{},
	})
	if err != nil {
		return e.fail(first, CodeUnsupported, err.Error())
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
	e.bridge = true
	e.output = true
	return nil
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
	if len(e.foreignPlans) == 0 {
		return ""
	}
	var out strings.Builder
	for i, plan := range e.foreignPlans {
		module := fmt.Sprintf("%sforeign%d", e.prefix, i)
		runtime := fmt.Sprintf("%spolyglot.Python{Environment:%s}", e.prefix, e.foreignEnvironment())
		if plan.Language == "typescript" {
			runtime = fmt.Sprintf("%spolyglot.TypeScript{}", e.prefix)
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
	if p == nil {
		return "nil"
	}
	return fmt.Sprintf("&%spolyglot.EnvironmentPlan{Language:%s,Name:%s,Root:%s,Dir:%s,Executable:%s,Manager:%s,RuntimeConstraint:%s,Manifests:%#v,Locks:%#v,PythonPath:%#v,Env:%#v,Explanation:%#v,ResolutionFiles:%#v,Fingerprint:%s}",
		e.prefix, strconv.Quote(p.Language), strconv.Quote(p.Name), strconv.Quote(p.Root), strconv.Quote(p.Dir), strconv.Quote(p.Executable), strconv.Quote(p.Manager), strconv.Quote(p.RuntimeConstraint), p.Manifests, p.Locks, p.PythonPath, p.Env, p.Explanation, p.ResolutionFiles, strconv.Quote(p.Fingerprint))
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
