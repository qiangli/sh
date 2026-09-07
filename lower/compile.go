package lower

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

type emitter struct {
	options         Options
	prefix          string
	marks           []Mapping
	scopes          []map[string]bool
	funcs           map[string]bool
	bridge          bool
	output          bool
	inFunc          bool
	globals         map[string]bool
	globalTypes     map[string]string
	typeNames       map[string]bool
	visibleGlobals  map[string]bool
	functionGlobals map[string]bool
	panicSupport    bool
	imports         map[string]string
	callableParams  map[*syntax.BashPPField]string
	resultTypes     []string
	dotNames        map[string]bool
	declaredGlobals map[string]bool
	iotaValue       *int
	bigIntegers     bool
	functionDecls   map[string]*syntax.BashPPFuncDecl
	enumMembers     map[string][]*syntax.Lit
	globalDecls     strings.Builder
}

// Compile returns canonical Go and mappings, or positioned diagnostics with no
// partial output. It never executes the input program.
func Compile(file *syntax.File, options Options) (*Result, error) {
	if err := CheckBashSharp(file); err != nil {
		return nil, err
	}
	return compilePass(file, options, nil)
}
func compilePass(file *syntax.File, options Options, globalTypes map[string]string) (*Result, error) {
	if file == nil {
		return nil, ErrorList{{Code: CodeUnsupported, Msg: "nil syntax file"}}
	}
	if options.Package == "" {
		options.Package = "main"
	}
	if options.Runtime == "" {
		options.Runtime = DefaultRuntime
	}
	if !token.IsIdentifier(options.Package) || token.Lookup(options.Package).IsKeyword() {
		return nil, ErrorList{{Code: CodeType, Msg: "invalid package name", Pos: file.Pos()}}
	}
	e := &emitter{functionDecls: map[string]*syntax.BashPPFuncDecl{}, enumMembers: map[string][]*syntax.Lit{}, options: options, funcs: map[string]bool{}, scopes: []map[string]bool{{}}, globals: map[string]bool{}, visibleGlobals: map[string]bool{}, imports: map[string]string{}, callableParams: map[*syntax.BashPPField]string{}, dotNames: map[string]bool{}, declaredGlobals: map[string]bool{}, typeNames: map[string]bool{}, globalTypes: globalTypes}
	// Allocate private names from the tree rather than reserving user identifiers.
	for n := 0; ; n++ {
		e.prefix = fmt.Sprintf("__bpp%d_", n)
		used := false
		syntax.Walk(file, func(n syntax.Node) bool {
			if l, ok := n.(*syntax.Lit); ok && strings.Contains(l.Value, e.prefix) {
				used = true
			}
			return true
		})
		if !used {
			break
		}
	}
	for _, s := range file.Stmts {
		if f, ok := s.Cmd.(*syntax.BashPPFuncDecl); ok {
			if f.Receiver == nil {
				e.funcs[f.Name.Value] = true
				e.functionDecls[f.Name.Value] = f
			}
		}
		switch n := s.Cmd.(type) {
		case *syntax.BashPPConstGroup:
			for _, spec := range n.Specs {
				e.globals[spec.Name.Value] = true
			}
		case *syntax.BashPPDecl:
			if n.Kw.Value == "type" {
				e.typeNames[n.Name.Value] = true
			}
			if n.Kw.Value == "var" || n.Kw.Value == "const" {
				e.globals[n.Name.Value] = true
			}
		case *syntax.BashPPShortDecl:
			for _, name := range names(n.Lhs) {
				if name != "_" {
					e.globals[name] = true
				}
			}
		}
	}
	if err := e.discoverCallableParams(file); err != nil {
		return nil, err
	}
	var declarations, body strings.Builder
	for _, s := range file.Stmts {
		if f, ok := s.Cmd.(*syntax.BashPPFuncDecl); ok {
			if err := e.statementFlags(s); err != nil {
				return nil, err
			}
			text, err := e.function(f)
			if err != nil {
				return nil, err
			}
			declarations.WriteString(text)
		} else {
			var text string
			var err error
			switch n := s.Cmd.(type) {
			case *syntax.BashPPImport:
				err = e.importDecl(n)
			case *syntax.BashPPConstGroup:
				var constants string
				constants, err = e.constGroup(n)
				declarations.WriteString(e.mark(n) + constants + "\n")
				for _, spec := range n.Specs {
					e.visibleGlobals[spec.Name.Value] = true
				}
			case *syntax.BashPPDecl:
				if n.Kw.Value == "var" || n.Kw.Value == "const" {
					text, err = e.globalStatement(s)
				} else if n.Kw.Value == "type" {
					var typ string
					typ, err = e.declarationType(n)
					declarations.WriteString(e.mark(n) + typ + "\n")
				} else {
					text, err = e.statement(s)
				}
			case *syntax.BashPPShortDecl:
				text, err = e.globalStatement(s)
			default:
				text, err = e.statement(s)
			}
			if err != nil {
				return nil, err
			}
			body.WriteString(text)
		}
	}
	imports := []string{}
	if e.bigIntegers {
		imports = append(imports, "math/big")
	}
	if e.panicSupport {
		e.output = true
		imports = append(imports, "os")
	}
	if e.output {
		imports = append(imports, "fmt")
	}
	if e.bridge {
		imports = append(imports, options.Runtime)
	}
	for _, path := range e.imports {
		found := false
		for _, p := range imports {
			if p == path {
				found = true
			}
		}
		if !found {
			imports = append(imports, path)
		}
	}
	sort.Strings(imports)
	var raw strings.Builder
	fmt.Fprintf(&raw, "// Code generated by mvdan.cc/sh/v3/lower. DO NOT EDIT.\npackage %s\n", options.Package)
	if e.output {
		fmt.Fprintf(&raw, "import %sfmt \"fmt\"\n", e.prefix)
	}
	if e.bridge {
		fmt.Fprintf(&raw, "import %srt %s\n", e.prefix, strconv.Quote(options.Runtime))
	}
	if e.panicSupport {
		fmt.Fprintf(&raw, "import %sos \"os\"\n", e.prefix)
	}
	if e.bigIntegers {
		fmt.Fprintf(&raw, "import %sbig \"math/big\"\n", e.prefix)
	}
	raw.WriteString(e.importLines())
	if e.panicSupport {
		raw.WriteString(e.panicHelpers())
	}
	raw.WriteString(e.globalDecls.String())
	raw.WriteString(declarations.String())
	tail := ""
	if e.bridge {
		tail = e.prefix + "rt.Exit()\n"
	}
	head := ""
	if e.panicSupport {
		head = e.panicBoundary()
	}
	fmt.Fprintf(&raw, "func main() {\n%s%s%s}\n", head, body.String(), tail)
	reset := ""
	if e.bridge {
		reset = e.prefix + "rt.Status = 0\n"
	}
	rawText := strings.ReplaceAll(raw.String(), "/*"+e.prefix+"reset*/", reset)
	status0 := ""
	status1 := ""
	if e.bridge {
		status0 = e.prefix + "rt.Status = 0;"
		status1 = e.prefix + "rt.Status = 1;"
	}
	rawText = strings.ReplaceAll(rawText, "/*"+e.prefix+"status0*/", status0)
	rawText = strings.ReplaceAll(rawText, "/*"+e.prefix+"status1*/", status1)
	source, err := format.Source([]byte(rawText))
	if err != nil {
		return nil, e.fail(file, CodeExpr, "generated Go is not syntactically valid: "+err.Error())
	}
	result := &Result{Source: source, Package: options.Package, Imports: imports, Origin: options.Origin}
	// Markers survive gofmt; map the next nonempty emitted line. Markers carry
	// only ordinal IDs, never source filenames or host paths.
	pending := -1
	for i, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "// lower:") {
			pending, _ = strconv.Atoi(strings.TrimPrefix(trimmed, "// lower:"))
			continue
		}
		if pending >= 0 && trimmed != "" {
			m := e.marks[pending]
			m.GoLine = i + 1
			m.GoCol = len(line) - len(strings.TrimLeft(line, "\t ")) + 1
			result.Mappings = append(result.Mappings, m)
			pending = -1
		}
	}
	// Native Go's checker supplies representability, arity and operator checks.
	// A synthetic bridge signature avoids loading module source or running tools.
	fs := token.NewFileSet()
	goFile, err := parser.ParseFile(fs, "generated.go", source, 0)
	if err != nil {
		return nil, e.fail(file, CodeExpr, err.Error())
	}
	var diagnostics ErrorList
	conf := types.Config{Importer: bridgeImporter{fallback: importer.Default(), path: options.Runtime}, Error: func(err error) {
		te, ok := err.(types.Error)
		pos := file.Pos()
		node := "File"
		if ok {
			line := fs.Position(te.Pos).Line
			for _, m := range result.Mappings {
				if m.GoLine > line {
					break
				}
				pos, node = m.Pos, m.Node
			}
		}
		msg := err.Error()
		if ok {
			msg = te.Msg
		}
		code := CodeType
		if strings.Contains(msg, "undefined:") {
			code = CodeUndefined
		}
		diagnostics = append(diagnostics, Diagnostic{Code: code, Msg: msg, Node: node, Pos: pos})
	}}
	checked, _ := conf.Check(options.Package, fs, []*ast.File{goFile}, nil)
	if len(diagnostics) > 0 {
		sort.SliceStable(diagnostics, func(i, j int) bool { return diagnostics[i].Pos.Offset() < diagnostics[j].Pos.Offset() })
		return nil, diagnostics
	}
	if globalTypes == nil && len(e.globals) > 0 {
		inferred := map[string]string{}
		for name := range e.globals {
			if obj := checked.Scope().Lookup(name); obj != nil {
				inferred[name] = types.TypeString(obj.Type(), func(p *types.Package) string {
					if p == checked {
						return ""
					}
					return p.Name()
				})
			}
		}
		return compilePass(file, options, inferred)
	}
	return result, nil
}

type bridgeImporter struct {
	fallback types.Importer
	path     string
}

func (i bridgeImporter) Import(path string) (*types.Package, error) {
	if path != i.path {
		return i.fallback.Import(path)
	}
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "bridge.go", `package shellrt
func Printf(format string,args ...any) error{return nil}
func Echo(args ...any) error{return nil}
func Word(v any) string{return ""}
func Fail(err error){}
func Exit(){}
var Status int
`, 0)
	if err != nil {
		return nil, err
	}
	return new(types.Config).Check(path, fs, []*ast.File{f}, nil)
}
func nodeName(n syntax.Node) string {
	if n == nil {
		return "nil"
	}
	return strings.TrimPrefix(reflect.TypeOf(n).String(), "*syntax.")
}
func (e *emitter) fail(n syntax.Node, code, msg string) error {
	var pos syntax.Pos
	if n != nil {
		pos = n.Pos()
	}
	return ErrorList{{Code: code, Msg: msg, Node: nodeName(n), Pos: pos}}
}
func (e *emitter) mark(n syntax.Node) string {
	id := len(e.marks)
	e.marks = append(e.marks, Mapping{Pos: n.Pos(), Node: nodeName(n)})
	return fmt.Sprintf("// lower:%d\n", id)
}
func (e *emitter) known(name string) bool {
	global := e.globals[name]
	if e.inFunc {
		global = e.functionGlobals[name]
	}
	if e.funcs[name] || global || e.typeNames[name] || e.imports[name] != "" || e.dotNames[name] {
		return true
	}
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if e.scopes[i][name] {
			return true
		}
	}
	switch name {
	case "true", "false", "nil":
		return true
	}
	return false
}

// bound distinguishes script storage from predeclared literals and callables.
func (e *emitter) bound(name string) bool {
	for i := len(e.scopes) - 1; i >= 0; i-- {
		if e.scopes[i][name] {
			return true
		}
	}
	if e.inFunc {
		return e.functionGlobals[name]
	}
	return e.globals[name]
}
func (e *emitter) bind(name string) {
	if name != "_" {
		e.scopes[len(e.scopes)-1][name] = true
	}
}
func (e *emitter) push() { e.scopes = append(e.scopes, map[string]bool{}) }
func (e *emitter) pop()  { e.scopes = e.scopes[:len(e.scopes)-1] }
func (e *emitter) statementFlags(s *syntax.Stmt) error {
	if s.Negated || s.Background || s.Coprocess || s.Disown || len(s.Redirs) > 0 {
		return e.fail(s, CodeUnsupported, "statement flags and redirections need the shell runtime slice")
	}
	return nil
}
func (e *emitter) statement(s *syntax.Stmt) (string, error) {
	if err := e.statementFlags(s); err != nil {
		return "", err
	}
	if s.Cmd == nil {
		return "", nil
	}
	text, err := e.command(s.Cmd)
	if err != nil {
		return "", err
	}
	reset := ""
	switch n := s.Cmd.(type) {
	case *syntax.BashPPDecl, *syntax.BashPPShortDecl, *syntax.BashPPAssign, *syntax.BashPPForAssign, *syntax.BashPPIncDec, *syntax.BashPPUpdate:
		reset = "/*" + e.prefix + "reset*/"
	case *syntax.BashPPReturn:
		if len(n.Results) > 0 {
			reset = "/*" + e.prefix + "reset*/"
		}
	}
	return e.mark(s.Cmd) + reset + text + "\n", nil
}
func (e *emitter) block(b *syntax.Block) (string, error) {
	e.push()
	defer e.pop()
	var out strings.Builder
	for _, s := range b.Stmts {
		x, err := e.statement(s)
		if err != nil {
			return "", err
		}
		out.WriteString(x)
	}
	return out.String(), nil
}
func names(lits []*syntax.Lit) []string {
	out := make([]string, len(lits))
	for i, l := range lits {
		out[i] = l.Value
	}
	return out
}
func (e *emitter) unused(ns []string) string {
	var out string
	for _, n := range ns {
		if n != "_" {
			out += "\n_ = " + n
		}
	}
	return out
}
func (e *emitter) function(f *syntax.BashPPFuncDecl) (string, error) {
	if f.Agentic != nil {
		return "", e.fail(f, CodeUnsupported, "marked, generic and receiver functions need callable metadata lowering")
	}
	// Package functions cannot see main locals. Do not accidentally resolve a
	// script-local name while lowering a package-level function.
	savedGlobals := e.functionGlobals
	e.functionGlobals = map[string]bool{}
	for name := range e.visibleGlobals {
		e.functionGlobals[name] = true
	}
	defer func() { e.functionGlobals = savedGlobals }()
	savedFunc := e.inFunc
	e.inFunc = true
	defer func() { e.inFunc = savedFunc }()
	saved := e.scopes
	e.scopes = []map[string]bool{{}}
	defer func() { e.scopes = saved }()
	if f.Receiver != nil {
		e.bind(f.Receiver.Name.Value)
	}
	for _, p := range f.TypeParams {
		for _, n := range p.Names {
			e.bind(n.Value)
		}
	}
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
	recv := ""
	if f.Receiver != nil {
		r := f.Receiver
		typ := r.RecvType.Value
		if r.Pointer {
			typ = "*" + typ
		}
		if len(r.TypeParams) > 0 {
			typ += "[" + strings.Join(names(r.TypeParams), ",") + "]"
		}
		recv = "(" + r.Name.Value + " " + typ + ") "
	}
	generics, err := e.typeParams(f.TypeParams)
	if err != nil {
		return "", err
	}
	return e.mark(f) + "func " + recv + e.goName(f.Name.Value) + generics + signature + " {\n" + body + "}\n", nil
}
func scalarType(s string) bool {
	switch s {
	case "bool", "string", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "float32", "float64", "byte", "rune", "error", "any":
		return true
	}
	return false
}
func (e *emitter) fields(fs []*syntax.BashPPField) (string, error) {
	var out []string
	for _, f := range fs {
		ns := names(f.Names)
		for _, n := range ns {
			e.bind(n)
		}
		s := strings.Join(ns, ",")
		if s != "" {
			s += " "
		}
		typ := "any"
		if f.FieldType != nil || f.FieldTypeExpr != nil {
			var err error
			if f.FieldType != nil && f.FieldType.Value == "func" {
				typ = e.callableParams[f]
				if typ == "" {
					return "", e.fail(f, CodeUnsupported, "func parameter needs an inferable native call-site signature")
				}
			} else {
				typ, err = e.fieldType(f)
			}
			if err != nil {
				return "", err
			}
		} else if f.Ellipsis.IsValid() {
			typ = "...any"
		}
		if f.Ellipsis.IsValid() && !strings.HasPrefix(typ, "...") {
			typ = "..." + typ
		}
		out = append(out, s+typ)
	}
	return strings.Join(out, ", "), nil
}
func (e *emitter) command(c syntax.Command) (string, error) {
	switch n := c.(type) {
	case *syntax.BashPPCommandCall:
		tail, err := e.call(n.Call)
		if err != nil {
			return "", err
		}
		return e.shellWithTail(&syntax.CallExpr{Args: n.Before}, tail)

	case *syntax.ForClause:
		return e.shellFor(n)
	case *syntax.BashPPConstGroup:
		return e.constGroup(n)
	case *syntax.BashPPRange:
		return e.rangeStmt(n)
	case *syntax.BashPPDecl:
		if n.Kw.Value != "var" && n.Kw.Value != "const" {
			return e.declarationType(n)
		}
		if len(n.TypeParams) > 0 || len(n.StructFields) > 0 || len(n.EnumMembers) > 0 {
			return "", e.fail(n, CodeUnsupported, "composite declaration")
		}
		typ := ""
		if n.DeclTypeExpr != nil {
			x, err := e.typeExpr(n.DeclTypeExpr)
			if err != nil {
				return "", err
			}
			typ = " " + x
		} else if n.DeclType != nil {
			x, err := e.typeSpelling(n.DeclType, n.DeclType.Value)
			if err != nil {
				return "", err
			}
			typ = " " + x
		}
		init := ""
		var err error
		if n.InitExpr != nil {
			init, err = e.expr(n.InitExpr)
		} else if len(n.Init) > 0 {
			init, err = e.wordSequence(n.Init)
		}
		if err != nil {
			return "", err
		}
		if init != "" {
			init = " = " + init
		}
		e.bind(n.Name.Value)
		return n.Kw.Value + " " + n.Name.Value + typ + init + e.unused([]string{n.Name.Value}), nil
	case *syntax.BashPPShortDecl:
		var rhs string
		var err error
		switch {
		case n.Call != nil:
			if len(n.Call.Fun) > 1 && e.imports[n.Call.Fun[0].Value] != "" {
				return "", e.fail(n, CodeUnsupported, "imported result binding needs object-projection metadata at the shell boundary")
			}
			rhs, err = e.call(n.Call)
		case n.Expr != nil:
			rhs, err = e.expr(n.Expr)
		case n.FuncLit != nil:
			rhs, err = e.literal(n.FuncLit)
		case len(n.MethodValue) > 0:
			rhs = strings.Join(names(n.MethodValue), ".")
		case n.MakeChan != nil || n.Recv != nil:
			return "", e.fail(n, CodeUnsupported, "callable/channel declaration variant")
		default:
			if len(n.Lhs) > 1 && len(n.Rhs) > 1 {
				var parts []string
				for _, w := range n.Rhs {
					x, problem := e.valueWord(w)
					if problem != nil {
						return "", problem
					}
					parts = append(parts, x)
				}
				rhs = strings.Join(parts, ", ")
			} else {
				rhs, err = e.wordSequence(n.Rhs)
			}
		}
		if err != nil {
			return "", err
		}
		if len(n.Lhs) == 1 && n.Call == nil && n.FuncLit == nil {
			if value, err := types.Eval(token.NewFileSet(), nil, token.NoPos, rhs); err == nil && value.Value != nil && value.Value.Kind() == constant.Int {
				if _, fits := constant.Int64Val(value.Value); !fits {
					e.bigIntegers = true
					rhs = "func() *" + e.prefix + "big.Int { v, _ := new(" + e.prefix + "big.Int).SetString(" + strconv.Quote(value.Value.ExactString()) + ",10); return v }()"
				}
			}
		}
		ns := names(n.Lhs)
		for _, name := range ns {
			e.bind(name)
		}
		post := ""
		if e.isRecover(n.Call) {
			if len(ns) != 1 {
				return "", e.fail(n, CodeResult, "recover yields one value")
			}
			post = e.recovered(ns[0])
		}
		return strings.Join(ns, ", ") + " := " + rhs + post + e.unused(ns), nil
	case *syntax.BashPPCall:
		if e.isRecover(n) {
			e.panicSupport = true
			return "if " + e.prefix + "recovered := recover(); " + e.prefix + "recovered == nil { /*" + e.prefix + "status1*/ } else { " + e.prefix + "popPanic(); /*" + e.prefix + "status0*/ }", nil
		}
		return e.call(n)
	case *syntax.BashPPReturn:
		if n.Call != nil {
			return "", e.fail(n, CodeUnsupported, "returned call requires interpreter callable result propagation")
		}
		if len(e.resultTypes) == 0 && len(n.Results) > 0 {
			if len(n.Results) != 1 {
				return "", e.fail(n, CodeResult, "resultless function return requires one status")
			}
			x, err := e.valueWord(n.Results[0])
			if err != nil {
				return "", err
			}
			e.bridge = true
			return e.prefix + "rt.Status = int(" + x + ")\nreturn", nil
		}
		if n.FuncLit != nil {
			x, err := e.literal(n.FuncLit)
			return "return " + x, err
		}
		var values []string
		for i, w := range n.Results {
			x, err := e.valueWord(w)
			if err != nil {
				return "", err
			}
			if i < len(e.resultTypes) && e.resultTypes[i] != "" {
				x = e.resultTypes[i] + "(" + x + ")"
			}
			values = append(values, x)
		}
		return "return " + strings.Join(values, ", "), nil
	case *syntax.BashPPForAssign:
		x, err := e.expr(n.Expr)
		return n.Name.Value + " = " + x, err
	case *syntax.BashPPAssign:
		if n.Call != nil {
			rhs, err := e.call(n.Call)
			if err != nil {
				return "", err
			}
			if len(n.Names) == 0 {
				return "", e.fail(n, CodeUnsupported, "structured call assignment")
			}
			return strings.Join(names(n.Names), ", ") + " = " + rhs, nil
		}
		if len(n.Names) > 0 {
			var rhs []string
			for _, x := range n.ValueExprs {
				v, err := e.expr(x)
				if err != nil {
					return "", err
				}
				rhs = append(rhs, v)
			}
			if len(rhs) == 0 {
				for _, w := range n.Values {
					v, err := e.valueWord(w)
					if err != nil {
						return "", err
					}
					rhs = append(rhs, v)
				}
			}
			return strings.Join(names(n.Names), ", ") + " = " + strings.Join(rhs, ", "), nil
		}
		if n.TargetExpr != nil && n.ValueExpr != nil {
			l, err := e.expr(n.TargetExpr)
			if err != nil {
				return "", err
			}
			r, err := e.expr(n.ValueExpr)
			return l + " = " + r, err
		}
		return "", e.fail(n, CodeUnsupported, "assignment variant lacks typed target/value")
	case *syntax.BashPPIncDec:
		target := ""
		var err error
		if n.Target != nil {
			target, err = e.expr(n.Target)
		} else if n.Name != nil {
			target = n.Name.Value
		} else {
			return "", e.fail(n, CodeUnsupported, "update target")
		}
		return target + n.Op.Value, err
	case *syntax.BashPPUpdate:
		if n.Target == nil || n.Value == nil {
			return "", e.fail(n, CodeExpr, "compound assignment lacks typed expressions")
		}
		l, err := e.expr(n.Target)
		if err != nil {
			return "", err
		}
		r, err := e.expr(n.Value)
		return l + " " + n.Op.Value + " " + r, err
	case *syntax.BashPPIf:
		return e.ifStmt(n)
	case *syntax.BashPPFor:
		return e.forStmt(n)
	case *syntax.BashPPSwitch:
		return e.switchStmt(n)
	case *syntax.BashPPBranch:
		return n.Kw.Value, nil
	case *syntax.BashPPDefer:
		if n.Call != nil && len(n.Call.Fun) == 1 && n.Call.Fun[0].Value == "panic" && !e.funcs["panic"] {
			if len(n.Call.Args) != 1 {
				return "", e.fail(n, CodeResult, "panic takes one argument")
			}
			e.panicSupport = true
			x, err := e.argument(n.Call.Args[0])
			return "defer func(v any) { panic(" + e.prefix + "pushPanic(v)) }(" + x + ")", err
		}
		x, err := e.call(n.Call)
		return "defer " + x, err
	case *syntax.Block:
		b, err := e.block(n)
		return "{\n" + b + "}", err
	case *syntax.CallExpr:
		return e.shell(n)
	default:
		return "", e.fail(c, CodeUnsupported, "certified node is not implemented by the foundation: "+nodeName(c))
	}
}
func (e *emitter) ifStmt(n *syntax.BashPPIf) (string, error) {
	e.push()
	defer e.pop()
	init := ""
	if n.Init != nil {
		x, err := e.command(n.Init)
		if err != nil {
			return "", err
		}
		init = x + "\n"
	}
	cond, err := e.expr(n.Cond)
	if err != nil {
		return "", err
	}
	body, err := e.block(n.Then)
	if err != nil {
		return "", err
	}
	out := "if " + cond + " {\n" + body + "}"
	if n.Else != nil {
		x, err := e.command(n.Else)
		if err != nil {
			return "", err
		}
		out += " else " + x
	}
	if init != "" {
		out = "{\n" + init + out + "\n}"
	}
	return out, nil
}
func (e *emitter) forStmt(n *syntax.BashPPFor) (string, error) {
	e.push()
	defer e.pop()
	init, post, cond := "", "", ""
	var err error
	if n.Init != nil {
		init, err = e.command(n.Init)
		if err != nil {
			return "", err
		}
	}
	if n.Post != nil {
		post, err = e.command(n.Post)
		if err != nil {
			return "", err
		}
	}
	if n.Cond != nil {
		cond, err = e.expr(n.Cond)
		if err != nil {
			return "", err
		}
	}
	body, err := e.block(n.Body)
	if err != nil {
		return "", err
	}
	header := cond
	if n.FirstSemi.IsValid() {
		if i := strings.Index(init, "\n_ = "); i >= 0 {
			init = init[:i]
		}
		header = init + "; " + cond + "; " + post
		init = ""
	}
	out := "for " + header + " {\n" + body + "}"
	if init != "" {
		out = "{\n" + init + "\n" + out + "\n}"
	}
	return out, nil
}
func (e *emitter) expr(x syntax.BashPPExpr) (string, error) {
	switch n := x.(type) {
	case *syntax.BashPPCall:
		return e.call(n)
	case *syntax.BashPPCompositeLit:
		return e.compositeExpr(n)
	case *syntax.BashPPSelectorExpr:
		return e.selectorExpr(n)
	case *syntax.BashPPTypeAssertExpr:
		return e.typeAssertExpr(n)
	case *syntax.BashPPSliceExpr:
		base, err := e.expr(n.X)
		if err != nil {
			return "", err
		}
		bounds := []string{"", ""}
		for i, x := range []syntax.BashPPExpr{n.Low, n.High} {
			if x != nil {
				bounds[i], err = e.expr(x)
				if err != nil {
					return "", err
				}
			}
		}
		if n.Max != nil {
			x, err := e.expr(n.Max)
			if err != nil {
				return "", err
			}
			bounds = append(bounds, x)
		}
		return base + "[" + strings.Join(bounds, ":") + "]", nil
	case *syntax.BashPPIndexExpr:
		a, err := e.expr(n.X)
		if err != nil {
			return "", err
		}
		b, err := e.expr(n.Index)
		return a + "[" + b + "]", err
	case *syntax.BashPPAddressExpr:
		a, err := e.expr(n.X)
		return "&" + a, err
	case *syntax.BashPPDerefExpr:
		a, err := e.expr(n.X)
		return "*" + a, err
	case *syntax.BashPPNewExpr:
		a, err := e.typeExpr(n.AllocType)
		return "new(" + a + ")", err
	case *syntax.BashPPBasicLit:
		return n.Value.Value, nil
	case *syntax.BashPPIdent:
		if n.Name.Value == "iota" && e.iotaValue != nil {
			return strconv.Itoa(*e.iotaValue), nil
		}
		if !e.known(n.Name.Value) {
			return "", e.fail(n, CodeUndefined, "undefined: "+n.Name.Value)
		}
		return e.goName(n.Name.Value), nil
	case *syntax.BashPPParenExpr:
		v, err := e.expr(n.X)
		return "(" + v + ")", err
	case *syntax.BashPPUnaryExpr:
		v, err := e.expr(n.X)
		return "(" + n.Op.Value + v + ")", err
	case *syntax.BashPPBinaryExpr:
		l, err := e.expr(n.X)
		if err != nil {
			return "", err
		}
		r, err := e.expr(n.Y)
		return "(" + l + " " + n.Op.Value + " " + r + ")", err
	case *syntax.BashPPConvertExpr:
		if !scalarType(n.ConvType.Value) {
			return "", e.fail(n, CodeUnsupported, "non-scalar conversion")
		}
		v, err := e.expr(n.X)
		return n.ConvType.Value + "(" + v + ")", err
	default:
		return "", e.fail(x, CodeUnsupported, "typed expression not implemented: "+nodeName(x))
	}
}
func (e *emitter) call(c *syntax.BashPPCall) (string, error) {
	if len(c.Fun) == 1 {
		if f := e.functionDecls[c.Fun[0].Value]; f != nil && (hasSharpDefaults(f) || len(c.ArgNames) > 0) {
			return e.sharpCall(c, f)
		}
	}
	if len(c.ArgNames) > 0 {
		return "", e.fail(c, CodeUnsupported, "named arguments require a resolved callable signature")
	}
	if c.FuncLit != nil {
		callee, err := e.literal(c.FuncLit)
		if err != nil {
			return "", err
		}
		var args []string
		for _, w := range c.Args {
			x, err := e.valueWord(w)
			if err != nil {
				return "", err
			}
			args = append(args, x)
		}
		return "(" + callee + ")(" + strings.Join(args, ",") + ")", nil
	}
	if len(c.Fun) == 0 {
		return "", e.fail(c, CodeExpr, "missing callable")
	}
	if len(c.Fun) > 1 {
		var args []string
		for _, w := range c.Args {
			x, err := e.argument(w)
			if err != nil {
				return "", err
			}
			args = append(args, x)
		}
		callee := strings.Join(names(c.Fun), ".")
		if c.PointerMethodExpr {
			callee = "(*" + c.Fun[0].Value + ")." + c.Fun[1].Value
		}
		spread := ""
		if c.Ellipsis.IsValid() {
			spread = "..."
		}
		return callee + "(" + strings.Join(args, ",") + spread + ")", nil
	}
	name := c.Fun[0].Value
	if !e.known(name) && !scalarType(name) && !nativeBuiltin(name) && name != "print" && name != "println" {
		return "", e.fail(c, CodeUndefined, "undefined callable: "+name)
	}
	var args []string
	if c.ArgType != nil {
		typ, err := e.typeExpr(c.ArgType)
		if err != nil {
			return "", err
		}
		args = append(args, typ)
	}
	for i, w := range c.Args {
		if i == 0 && c.ArgType != nil {
			continue
		}
		if i == 0 && name == "make" && !e.funcs[name] && c.ArgType == nil {
			var spelling strings.Builder
			_ = syntax.NewPrinter().Print(&spelling, w)
			typ, err := e.typeSpelling(w, spelling.String())
			if err != nil {
				return "", err
			}
			args = append(args, typ)
			continue
		}
		x, err := e.argument(w)
		if err != nil {
			return "", err
		}
		args = append(args, x)
	}
	if members := e.enumMembers[name]; len(members) > 0 && len(args) == 1 {
		if _, err := strconv.ParseInt(args[0], 0, 64); err != nil {
			return "", e.fail(c, CodeUnsupported, "nonconstant enum conversion requires runtime membership guard")
		}
	}
	if !e.funcs[name] && (name == "print" || name == "println") {
		e.output = true
		if name == "println" {
			return e.prefix + "fmt.Println(" + strings.Join(args, ", ") + ")", nil
		}
		for i, a := range args {
			args[i] = e.prefix + "fmt.Sprint(" + a + ")"
		}
		return e.prefix + "fmt.Print(" + strings.Join(args, ", ") + ")", nil
	}
	spread := ""
	if c.Ellipsis.IsValid() {
		spread = "..."
	}
	if !e.funcs[name] && name == "panic" {
		if len(args) != 1 {
			return "", e.fail(c, CodeResult, "panic takes one argument")
		}
		e.panicSupport = true
		return "panic(" + e.prefix + "pushPanic(" + args[0] + "))", nil
	}
	if !e.funcs[name] && name == "recover" {
		if len(args) != 0 {
			return "", e.fail(c, CodeResult, "recover takes no arguments")
		}
		e.panicSupport = true
		return "recover()", nil
	}
	typeargs, err := e.typeArgs(c.TypeArgs)
	if err != nil {
		return "", err
	}
	return e.goName(name) + typeargs + "(" + strings.Join(args, ", ") + spread + ")", nil
}
