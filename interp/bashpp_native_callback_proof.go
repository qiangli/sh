package interp

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const dependencyCallbackProofDiagnosticEnv = "BASHPP_CALLBACK_PROOF_DIAG"
const dependencyCallbackProofDiagnosticLimit = 48

// dependencyCallbackProofStepBudget bounds the statements one proof may walk.
// Every proof starts at this budget; only the test-only refusal enumeration
// raises it, and only to see past the first refusal it records.
const dependencyCallbackProofStepBudget = 20000

// dependencyCallbackProofDepthBound bounds the nesting of function bodies one
// proof may descend through. Every proof starts at this bound; only the
// test-only refusal enumeration raises it, and only to measure how deep the
// walk would have had to go.
const dependencyCallbackProofDepthBound = 64

// dependencyFunctionCallbackLifetimeProof proves, from the exact package
// sources selected for the dependency worker, that a package function cannot
// retain or asynchronously invoke any original callback argument. A missing
// source, unresolved call, unsupported syntax, or exhausted bound is a refusal.
func dependencyFunctionCallbackLifetimeProof(ctx context.Context, req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	if q.Op != "call" || q.Receiver != nil {
		return false
	}
	alias, name, ok := strings.Cut(q.Selector, ".")
	if !ok || req.Imports[alias] == "" || name == "" {
		return false
	}
	var callbackArgs []int
	for i, arg := range q.Args {
		if arg.Kind == "callback" {
			callbackArgs = append(callbackArgs, i)
		}
	}
	if len(callbackArgs) == 0 {
		return false
	}
	var key strings.Builder
	key.WriteString(req.Imports[alias])
	key.WriteByte('.')
	key.WriteString(name)
	for _, index := range callbackArgs {
		key.WriteByte(':')
		key.WriteString(strconv.Itoa(index))
	}
	cacheKey := key.String()
	if req.Bridge != nil {
		req.Bridge.mu.Lock()
		seen, result := req.Bridge.callbackProofSeen[cacheKey], req.Bridge.callbackProofs[cacheKey]
		req.Bridge.mu.Unlock()
		if seen {
			return result
		}
	}
	result := loadDependencyFunctionCallbackLifetimeProof(ctx, req, req.Imports[alias], name, callbackArgs)
	if req.Bridge != nil && ctx.Err() == nil {
		req.Bridge.mu.Lock()
		if req.Bridge.callbackProofSeen == nil {
			req.Bridge.callbackProofSeen = make(map[string]bool)
			req.Bridge.callbackProofs = make(map[string]bool)
		}
		req.Bridge.callbackProofSeen[cacheKey] = true
		req.Bridge.callbackProofs[cacheKey] = result
		req.Bridge.mu.Unlock()
	}
	return result
}

func loadDependencyFunctionCallbackLifetimeProof(ctx context.Context, req bashPPEvalRequest, path, name string, callbackArgs []int) bool {
	facts, err := bashPPGoListFacts(ctx, req, path)
	if err != nil || facts.Dir == "" || len(facts.CgoFiles) != 0 || len(facts.GoFiles) == 0 {
		return false
	}
	files := make([]*ast.File, 0, len(facts.GoFiles))
	fset := token.NewFileSet()
	for _, file := range facts.GoFiles {
		if filepath.IsAbs(file) || filepath.Base(file) != file {
			return false
		}
		data, err := os.ReadFile(filepath.Join(facts.Dir, file))
		if err != nil {
			return false
		}
		parsed, err := parser.ParseFile(fset, file, data, parser.SkipObjectResolution)
		if err != nil {
			return false
		}
		files = append(files, parsed)
	}
	proof := newDependencyCallbackProof(files)
	proof.fset = fset
	proof.diagnostics = os.Getenv(dependencyCallbackProofDiagnosticEnv) != ""
	result := proof.prove(name, callbackArgs)
	if !result && proof.diagnostics {
		proof.writeDiagnostics(os.Stderr, path, name)
	}
	return result
}

type dependencyCallbackObject struct {
	typ       string
	lineage   *dependencyCallbackObject
	owned     bool
	escaped   bool
	synthetic bool
	scalar    bool
	general   bool
	fields    map[string]dependencyCallbackValue

	// boundMethod, when non-nil, marks this object as a method value: calling
	// it runs boundMethod's body with the receiver stored under
	// dependencyCallbackElementField. A join of two method values drops the
	// binding, and a tainted callee without one refuses, so losing the field
	// is fail-closed rather than an admit.
	boundMethod *ast.FuncDecl
}

type dependencyCallbackClosure struct {
	lit      *ast.FuncLit
	lineage  *dependencyCallbackClosure
	env      map[string]dependencyCallbackValue
	captures map[string]bool
}

type dependencyCallbackCell struct {
	lineage *dependencyCallbackCell
	value   dependencyCallbackValue
}

type dependencyCallbackDeferred struct {
	call      *ast.CallExpr
	args      []dependencyCallbackValue
	callee    dependencyCallbackValue
	hasCallee bool
}

type dependencyCallbackValue struct {
	callback *dependencyCallbackClosure // nil lit means the original callback
	object   *dependencyCallbackObject
	cell     *dependencyCallbackCell
	callable bool // source declares this clean value to have function type
	escaped  bool
}

const dependencyCallbackElementField = "[]"

func (v dependencyCallbackValue) tainted() bool {
	if v.cell != nil {
		return v.cell.value.tainted()
	}
	if v.callback != nil {
		return true
	}
	return dependencyCallbackObjectTainted(v.object, map[*dependencyCallbackObject]bool{})
}

func dependencyCallbackResolvedValue(value dependencyCallbackValue) dependencyCallbackValue {
	seen := make(map[*dependencyCallbackCell]bool)
	for value.cell != nil && !seen[value.cell] {
		seen[value.cell] = true
		value = value.cell.value
	}
	return value
}

func dependencyCallbackObjectTainted(obj *dependencyCallbackObject, seen map[*dependencyCallbackObject]bool) bool {
	if obj == nil || seen[obj] {
		return false
	}
	seen[obj] = true
	for _, field := range obj.fields {
		field = dependencyCallbackResolvedValue(field)
		if field.callback != nil || dependencyCallbackObjectTainted(field.object, seen) {
			return true
		}
	}
	return false
}

type dependencyCallbackType struct {
	fields   map[string]dependencyCallbackField
	order    []string
	embedded []string
}

type dependencyCallbackField struct {
	typ      string
	callable bool
	scalar   bool
}

type dependencyCallbackFormal struct {
	name     string
	variadic bool
}

// dependencyCallbackLabel records one label declared directly by a statement
// list. index is the label's position in list; declared and scoped are the
// scope facts that list walks with, so a goto edge can re-run the suffix the
// label starts under the same lexical rules.
type dependencyCallbackLabel struct {
	list     []ast.Stmt
	index    int
	declared map[string]bool
	scoped   bool
	entered  bool
}

// dependencyCallbackLabelCheck is the back-edge obligation of one label that a
// backward goto jumps to: the taint of every binding live where the label
// starts, to be compared once the region the label heads has been walked.
type dependencyCallbackLabelCheck struct {
	labeled *ast.LabeledStmt
	before  map[string]bool
}

type dependencyCallbackActiveFrame struct {
	supplied         map[string]dependencyCallbackValue
	receiver         dependencyCallbackValue
	argSnapshots     map[string]string
	receiverSnapshot string
	diagnosticFrame  map[string]string
}

// dependencyCallbackRefusalSite is one refusal the proof reached, handed to
// the test-only enumerate hook so a measurement run can group the sites the
// production proof would only ever report one of.
type dependencyCallbackRefusalSite struct {
	function string
	position string
	reason   string
	store    string
	line     string
}

type dependencyCallbackCompletedFrame struct {
	before string
	after  string
}

type dependencyCallbackProof struct {
	funcs     map[string][]*ast.FuncDecl
	methods   map[string]map[string]*ast.FuncDecl
	types     map[string]dependencyCallbackType
	typeSpecs map[string]*ast.TypeSpec
	funcTypes map[string]bool
	// imports holds every package name the proved files import, by declared
	// alias or path basename. A selector whose base names one of these is a
	// package-level call to code outside the proved sources, never interface
	// dispatch over them.
	imports map[string]bool
	globals map[string]bool
	consts  map[string]bool
	active  map[string]dependencyCallbackActiveFrame
	done    map[string][]dependencyCallbackCompletedFrame
	results []map[string]bool
	defers  [][]dependencyCallbackDeferred
	steps   int

	// labels holds every label declared by a statement list currently being
	// walked, so that a goto nested inside it resolves to an edge. Go forbids
	// a goto from leaving the function that declares its label, so the map is
	// saved and cleared whenever a new function body is entered.
	labels map[string]*dependencyCallbackLabel

	// summarizing counts the functions whose generalized recursive body is
	// being summarized right now; summaries caches the verdict of every
	// summary already charged to the budget. A generalized recursive edge is a
	// fixpoint, so re-entering the summary of a function already on the
	// summary stack would re-walk the same statements under the same
	// generalized frame: the enclosing walk already carries that obligation.
	summarizing map[string]int
	summaries   map[string]bool

	funcSteps map[string]int
	probe     func(*dependencyCallbackProof)

	// stepLimit is the statement budget of this proof, always
	// dependencyCallbackProofStepBudget in production.
	stepLimit int

	// depthLimit is the body-nesting bound of this proof, always
	// dependencyCallbackProofDepthBound in production. maxDepth records the
	// deepest body the walk actually entered, which is what a measurement run
	// reads back.
	depthLimit int
	maxDepth   int

	// enumerate, when non-nil, replaces the verdict of every refusal with
	// whatever it returns, so a measurement run can record a refusal site and
	// then keep walking its siblings. Production leaves it nil, which is the
	// only configuration in which refuse reports a refusal.
	enumerate func(dependencyCallbackRefusalSite) bool

	fset        *token.FileSet
	currentFunc string
	reason      string
	storeKind   string
	storeLHS    string
	storeRHS    string
	diagnostics bool
	diagnostic  []string
}

func newDependencyCallbackProof(files []*ast.File) *dependencyCallbackProof {
	p := &dependencyCallbackProof{
		funcs: make(map[string][]*ast.FuncDecl), methods: make(map[string]map[string]*ast.FuncDecl),
		types: make(map[string]dependencyCallbackType), typeSpecs: make(map[string]*ast.TypeSpec), funcTypes: make(map[string]bool), globals: make(map[string]bool), consts: make(map[string]bool), active: make(map[string]dependencyCallbackActiveFrame), done: make(map[string][]dependencyCallbackCompletedFrame),
		imports:   make(map[string]bool),
		stepLimit: dependencyCallbackProofStepBudget, depthLimit: dependencyCallbackProofDepthBound,
	}
	for _, file := range files {
		for _, imp := range file.Imports {
			name := ""
			if imp.Name != nil {
				name = imp.Name.Name
			} else if path, err := strconv.Unquote(imp.Path.Value); err == nil {
				if slash := strings.LastIndex(path, "/"); slash >= 0 {
					path = path[slash+1:]
				}
				name = path
			}
			if name != "" && name != "_" && name != "." {
				p.imports[name] = true
			}
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, item := range gen.Specs {
				spec, ok := item.(*ast.TypeSpec)
				if !ok {
					continue
				}
				p.typeSpecs[spec.Name.Name] = spec
				if _, ok := spec.Type.(*ast.FuncType); ok {
					p.funcTypes[spec.Name.Name] = true
				}
			}
		}
	}
	for _, file := range files {
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Recv == nil {
					p.funcs[decl.Name.Name] = append(p.funcs[decl.Name.Name], decl)
					continue
				}
				typ := dependencyCallbackTypeName(decl.Recv.List[0].Type)
				if typ == "" {
					continue
				}
				if p.methods[typ] == nil {
					p.methods[typ] = make(map[string]*ast.FuncDecl)
				}
				p.methods[typ][decl.Name.Name] = decl
			case *ast.GenDecl:
				for _, item := range decl.Specs {
					switch spec := item.(type) {
					case *ast.TypeSpec:
						st, ok := spec.Type.(*ast.StructType)
						if !ok {
							continue
						}
						shape := dependencyCallbackType{fields: make(map[string]dependencyCallbackField)}
						for _, field := range st.Fields.List {
							fieldType := dependencyCallbackTypeName(field.Type)
							if len(field.Names) == 0 {
								if fieldType != "" {
									shape.embedded = append(shape.embedded, fieldType)
									shape.order = append(shape.order, fieldType)
								}
								continue
							}
							_, directFunc := field.Type.(*ast.FuncType)
							scalar := p.certifiedScalarType(field.Type, make(map[string]bool), 0)
							for _, name := range field.Names {
								shape.fields[name.Name] = dependencyCallbackField{typ: fieldType, callable: directFunc || p.funcTypes[fieldType], scalar: scalar}
								shape.order = append(shape.order, name.Name)
							}
						}
						p.types[spec.Name.Name] = shape
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							if decl.Tok == token.CONST {
								if len(spec.Names) == 1 && len(spec.Values) == 1 {
									if ident, ok := spec.Values[0].(*ast.Ident); ok && (ident.Name == "true" || ident.Name == "false") {
										p.consts[name.Name] = ident.Name == "true"
									}
								}
							} else {
								p.globals[name.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return p
}

func (p *dependencyCallbackProof) certifiedScalarType(expr ast.Expr, seen map[string]bool, depth int) bool {
	if expr == nil || depth > 32 {
		return false
	}
	switch expr := expr.(type) {
	case *ast.Ident:
		if spec := p.typeSpecs[expr.Name]; spec != nil {
			if seen[expr.Name] || spec.TypeParams != nil && len(spec.TypeParams.List) != 0 {
				return false
			}
			seen[expr.Name] = true
			return p.certifiedScalarType(spec.Type, seen, depth+1)
		}
		switch expr.Name {
		case "bool", "string",
			"int", "int8", "int16", "int32", "int64",
			"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
			"byte", "rune",
			"float32", "float64",
			"complex64", "complex128":
			return true
		}
	case *ast.ParenExpr:
		return p.certifiedScalarType(expr.X, seen, depth)
	}
	return false
}

func dependencyCallbackTypeName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return dependencyCallbackTypeName(expr.X)
	case *ast.IndexExpr:
		return dependencyCallbackTypeName(expr.X)
	case *ast.IndexListExpr:
		return dependencyCallbackTypeName(expr.X)
	}
	return ""
}

func (p *dependencyCallbackProof) prove(name string, callbackArgs []int) bool {
	decls := p.funcs[name]
	if len(decls) != 1 || decls[0].Body == nil {
		p.refuse(nil, fmt.Sprintf("expected exactly one function body for %s, found %d", name, len(decls)))
		return false
	}
	env := make(map[string]dependencyCallbackValue)
	params, valid := dependencyCallbackFormals(decls[0].Type.Params)
	if !valid {
		p.refuse(decls[0], fmt.Sprintf("function %s has invalid formal parameters %q", decls[0].Name.Name, dependencyCallbackFormalLabels(decls[0].Type.Params)))
		return false
	}
	variadic := len(params) != 0 && params[len(params)-1].variadic
	for _, index := range callbackArgs {
		formal := index
		if variadic && formal >= len(params)-1 {
			formal = len(params) - 1
		}
		if formal < 0 || formal >= len(params) || params[formal].name == "" {
			p.refuse(decls[0], fmt.Sprintf("callback argument index %d has no named parameter", index))
			return false
		}
		env[params[formal].name] = dependencyCallbackValue{callback: &dependencyCallbackClosure{}}
	}
	return p.function(decls[0], env, dependencyCallbackValue{}, 0)
}

func (p *dependencyCallbackProof) refuse(node ast.Node, reason string) bool {
	function := p.currentFunc
	if function == "" {
		function = "<package>"
	}
	pos := "<unknown>"
	if p.fset != nil && node != nil && node.Pos().IsValid() {
		pos = p.fset.Position(node.Pos()).String()
	}
	line := fmt.Sprintf("%s: function=%s: %s", pos, function, reason)
	if p.storeKind != "" {
		line += fmt.Sprintf("; store=%s lhs=%s rhs=%s", p.storeKind, p.storeLHS, p.storeRHS)
	}
	if p.enumerate != nil {
		return p.enumerate(dependencyCallbackRefusalSite{function: function, position: pos, reason: reason, store: p.storeKind, line: line})
	}
	if p.reason == "" {
		p.reason = line
	}
	if p.diagnostics && len(p.diagnostic) < dependencyCallbackProofDiagnosticLimit {
		p.diagnostic = append(p.diagnostic, line)
	}
	return false
}

func (p *dependencyCallbackProof) diagnoseRecursiveFrameDifference(key string, active dependencyCallbackActiveFrame, supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue) {
	if !p.diagnostics {
		return
	}
	p.addDiagnostic("recursive frame difference for " + key)
	left := dependencyCallbackCloneDiagnosticFrame(active.diagnosticFrame)
	right := dependencyCallbackDiagnosticFrame(supplied, receiver)
	for _, path := range dependencyCallbackDiagnosticDifferingPaths(left, right, 12) {
		p.addDiagnostic("  " + path + " active=" + left[path] + " current=" + right[path])
	}
}

func (p *dependencyCallbackProof) addDiagnostic(line string) {
	if p.diagnostics && len(p.diagnostic) < dependencyCallbackProofDiagnosticLimit {
		p.diagnostic = append(p.diagnostic, line)
	}
}

func (p *dependencyCallbackProof) writeDiagnostics(out *os.File, path, name string) {
	if p.reason == "" {
		p.refuse(nil, "callback proof refused without a recorded inner reason")
	}
	fmt.Fprintf(out, "gosource callback proof refused %s.%s\n", path, name)
	for _, line := range p.diagnostic {
		fmt.Fprintln(out, line)
	}
	if len(p.diagnostic) == 0 {
		fmt.Fprintln(out, p.reason)
	}
}

func dependencyCallbackFieldNames(fields *ast.FieldList) []string {
	formals, _ := dependencyCallbackFormals(fields)
	names := make([]string, len(formals))
	for i, formal := range formals {
		names[i] = formal.name
	}
	return names
}

func dependencyCallbackFuncLitCaptures(lit *ast.FuncLit, env map[string]dependencyCallbackValue) map[string]bool {
	if lit == nil || lit.Body == nil || len(env) == 0 {
		return nil
	}
	scope := make(map[string]bool)
	for _, name := range dependencyCallbackFieldNames(lit.Type.Params) {
		if name != "" {
			scope[name] = true
		}
	}
	for _, name := range dependencyCallbackFieldNames(lit.Type.Results) {
		if name != "" {
			scope[name] = true
		}
	}
	captures := make(map[string]bool)
	dependencyCallbackCaptureBlock(lit.Body, scope, env, captures)
	if len(captures) == 0 {
		return nil
	}
	return captures
}

func dependencyCallbackCaptureBlock(block *ast.BlockStmt, scope map[string]bool, env map[string]dependencyCallbackValue, captures map[string]bool) {
	if block == nil {
		return
	}
	local := dependencyCallbackCloneScope(scope)
	for _, stmt := range block.List {
		dependencyCallbackCaptureStmt(stmt, local, env, captures)
	}
}

func dependencyCallbackCaptureStmt(stmt ast.Stmt, scope map[string]bool, env map[string]dependencyCallbackValue, captures map[string]bool) {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		for _, rhs := range stmt.Rhs {
			dependencyCallbackCaptureExpr(rhs, scope, env, captures)
		}
		for _, lhs := range stmt.Lhs {
			if stmt.Tok == token.DEFINE {
				if _, ok := lhs.(*ast.Ident); ok {
					continue
				}
			}
			dependencyCallbackCaptureExpr(lhs, scope, env, captures)
		}
		if stmt.Tok == token.DEFINE {
			for _, lhs := range stmt.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
					scope[ident.Name] = true
				}
			}
		}
	case *ast.DeclStmt:
		decl, ok := stmt.Decl.(*ast.GenDecl)
		if !ok {
			return
		}
		for _, item := range decl.Specs {
			spec, ok := item.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, value := range spec.Values {
				dependencyCallbackCaptureExpr(value, scope, env, captures)
			}
			for _, name := range spec.Names {
				if name.Name != "_" {
					scope[name.Name] = true
				}
			}
		}
	case *ast.ExprStmt:
		dependencyCallbackCaptureExpr(stmt.X, scope, env, captures)
	case *ast.ReturnStmt:
		for _, result := range stmt.Results {
			dependencyCallbackCaptureExpr(result, scope, env, captures)
		}
	case *ast.GoStmt:
		dependencyCallbackCaptureExpr(stmt.Call, scope, env, captures)
	case *ast.DeferStmt:
		dependencyCallbackCaptureExpr(stmt.Call, scope, env, captures)
	case *ast.SendStmt:
		dependencyCallbackCaptureExpr(stmt.Chan, scope, env, captures)
		dependencyCallbackCaptureExpr(stmt.Value, scope, env, captures)
	case *ast.IfStmt:
		next := dependencyCallbackCloneScope(scope)
		if stmt.Init != nil {
			dependencyCallbackCaptureStmt(stmt.Init, next, env, captures)
		}
		dependencyCallbackCaptureExpr(stmt.Cond, next, env, captures)
		dependencyCallbackCaptureBlock(stmt.Body, next, env, captures)
		if stmt.Else != nil {
			dependencyCallbackCaptureStmt(stmt.Else, next, env, captures)
		}
	case *ast.BlockStmt:
		dependencyCallbackCaptureBlock(stmt, scope, env, captures)
	case *ast.ForStmt:
		next := dependencyCallbackCloneScope(scope)
		if stmt.Init != nil {
			dependencyCallbackCaptureStmt(stmt.Init, next, env, captures)
		}
		if stmt.Cond != nil {
			dependencyCallbackCaptureExpr(stmt.Cond, next, env, captures)
		}
		dependencyCallbackCaptureBlock(stmt.Body, next, env, captures)
		if stmt.Post != nil {
			dependencyCallbackCaptureStmt(stmt.Post, next, env, captures)
		}
	case *ast.RangeStmt:
		dependencyCallbackCaptureExpr(stmt.X, scope, env, captures)
		next := dependencyCallbackCloneScope(scope)
		if stmt.Tok == token.DEFINE {
			for _, expr := range []ast.Expr{stmt.Key, stmt.Value} {
				if ident, ok := expr.(*ast.Ident); ok && ident.Name != "_" {
					next[ident.Name] = true
				}
			}
		} else {
			dependencyCallbackCaptureExpr(stmt.Key, next, env, captures)
			dependencyCallbackCaptureExpr(stmt.Value, next, env, captures)
		}
		dependencyCallbackCaptureBlock(stmt.Body, next, env, captures)
	case *ast.SwitchStmt:
		next := dependencyCallbackCloneScope(scope)
		if stmt.Init != nil {
			dependencyCallbackCaptureStmt(stmt.Init, next, env, captures)
		}
		if stmt.Tag != nil {
			dependencyCallbackCaptureExpr(stmt.Tag, next, env, captures)
		}
		dependencyCallbackCaptureCaseClauses(stmt.Body, next, env, captures)
	case *ast.TypeSwitchStmt:
		next := dependencyCallbackCloneScope(scope)
		if stmt.Init != nil {
			dependencyCallbackCaptureStmt(stmt.Init, next, env, captures)
		}
		if stmt.Assign != nil {
			dependencyCallbackCaptureStmt(stmt.Assign, next, env, captures)
		}
		dependencyCallbackCaptureCaseClauses(stmt.Body, next, env, captures)
	case *ast.SelectStmt:
		if stmt.Body != nil {
			for _, item := range stmt.Body.List {
				clause, ok := item.(*ast.CommClause)
				if !ok {
					continue
				}
				next := dependencyCallbackCloneScope(scope)
				if clause.Comm != nil {
					dependencyCallbackCaptureStmt(clause.Comm, next, env, captures)
				}
				dependencyCallbackCaptureBlock(&ast.BlockStmt{List: clause.Body}, next, env, captures)
			}
		}
	case *ast.LabeledStmt:
		dependencyCallbackCaptureStmt(stmt.Stmt, scope, env, captures)
	case *ast.IncDecStmt:
		dependencyCallbackCaptureExpr(stmt.X, scope, env, captures)
	}
}

func dependencyCallbackCaptureCaseClauses(body *ast.BlockStmt, scope map[string]bool, env map[string]dependencyCallbackValue, captures map[string]bool) {
	if body == nil {
		return
	}
	for _, item := range body.List {
		clause, ok := item.(*ast.CaseClause)
		if !ok {
			continue
		}
		next := dependencyCallbackCloneScope(scope)
		for _, expr := range clause.List {
			dependencyCallbackCaptureExpr(expr, next, env, captures)
		}
		dependencyCallbackCaptureBlock(&ast.BlockStmt{List: clause.Body}, next, env, captures)
	}
}

func dependencyCallbackCaptureExpr(expr ast.Expr, scope map[string]bool, env map[string]dependencyCallbackValue, captures map[string]bool) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		switch node := node.(type) {
		case *ast.FuncLit:
			dependencyCallbackCaptureFuncLit(node, scope, env, captures)
			return false
		case *ast.SelectorExpr:
			dependencyCallbackCaptureExpr(node.X, scope, env, captures)
			return false
		case *ast.Ident:
			if node.Name == "_" || scope[node.Name] {
				return true
			}
			if _, ok := env[node.Name]; ok {
				captures[node.Name] = true
			}
		}
		return true
	})
}

func dependencyCallbackCaptureFuncLit(lit *ast.FuncLit, scope map[string]bool, env map[string]dependencyCallbackValue, captures map[string]bool) {
	if lit == nil || lit.Body == nil {
		return
	}
	nested := dependencyCallbackCloneScope(scope)
	for _, name := range dependencyCallbackFieldNames(lit.Type.Params) {
		if name != "" {
			nested[name] = true
		}
	}
	for _, name := range dependencyCallbackFieldNames(lit.Type.Results) {
		if name != "" {
			nested[name] = true
		}
	}
	dependencyCallbackCaptureBlock(lit.Body, nested, env, captures)
}

func dependencyCallbackFormals(fields *ast.FieldList) ([]dependencyCallbackFormal, bool) {
	if fields == nil {
		return nil, true
	}
	var formals []dependencyCallbackFormal
	for i, field := range fields.List {
		_, variadic := field.Type.(*ast.Ellipsis)
		if variadic && (i != len(fields.List)-1 || len(field.Names) > 1) {
			return nil, false
		}
		if len(field.Names) == 0 {
			formals = append(formals, dependencyCallbackFormal{variadic: variadic})
			continue
		}
		for _, name := range field.Names {
			formals = append(formals, dependencyCallbackFormal{name: name.Name, variadic: variadic})
		}
	}
	return formals, true
}

func dependencyCallbackFormalLabels(fields *ast.FieldList) []string {
	formals, ok := dependencyCallbackFormals(fields)
	if !ok {
		return []string{"<invalid>"}
	}
	labels := make([]string, len(formals))
	for i, formal := range formals {
		labels[i] = formal.name
		if labels[i] == "" {
			labels[i] = "<unnamed>"
		}
		if formal.variadic {
			labels[i] += "..."
		}
	}
	return labels
}

func dependencyCallbackBindArguments(fields *ast.FieldList, args []dependencyCallbackValue) (map[string]dependencyCallbackValue, bool) {
	formals, ok := dependencyCallbackFormals(fields)
	if !ok {
		return nil, false
	}
	variadic := len(formals) != 0 && formals[len(formals)-1].variadic
	fixed := len(formals)
	if variadic {
		fixed--
		if len(args) < fixed {
			return nil, false
		}
	} else if len(args) != fixed {
		return nil, false
	}
	supplied := make(map[string]dependencyCallbackValue)
	for i := 0; i < fixed; i++ {
		if formals[i].name != "" {
			supplied[formals[i].name] = args[i]
		}
	}
	if variadic && formals[fixed].name != "" {
		for _, value := range args[fixed:] {
			if value.tainted() {
				supplied[formals[fixed].name] = value
				break
			}
		}
	}
	return supplied, true
}

func (p *dependencyCallbackProof) function(decl *ast.FuncDecl, supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue, depth int) bool {
	if decl == nil || decl.Body == nil || depth > p.depthLimit || p.steps > p.stepLimit {
		return p.refuse(decl, fmt.Sprintf("function unavailable or proof bounds exceeded depth=%d steps=%d", depth, p.steps))
	}
	if depth > p.maxDepth {
		p.maxDepth = depth
	}
	key := decl.Name.Name
	if decl.Recv != nil {
		key = dependencyCallbackTypeName(decl.Recv.List[0].Type) + "." + key
	}
	if active, ok := p.active[key]; ok {
		// A recursive edge may only preserve callback-bearing state exactly or
		// grow callback-free regions that are then forbidden to receive callbacks.
		if same, generalized, rejection := dependencyCallbackSameFrame(active, supplied, receiver); same {
			if generalized && p.recursiveBodyStoresCallback(decl, key, supplied, receiver, depth+1) {
				return p.refuse(decl, "generalized recursive body may store callback-bearing value")
			}
			return true
		} else if rejection != "" {
			p.addDiagnostic("same-frame rejection: " + rejection)
		}
		p.diagnoseRecursiveFrameDifference(key, active, supplied, receiver)
		return p.refuse(decl, "recursive call changes callback capture state")
	}
	before := dependencyCallbackFrameSnapshot(supplied, receiver)
	for _, frame := range p.done[key] {
		if frame.before == before && frame.after == before {
			return true
		}
	}
	p.active[key] = dependencyCallbackNewActiveFrame(supplied, receiver)
	if p.diagnostics {
		active := p.active[key]
		active.diagnosticFrame = dependencyCallbackDiagnosticFrame(supplied, receiver)
		p.active[key] = active
	}
	defer delete(p.active, key)
	savedFunc := p.currentFunc
	p.currentFunc = key
	savedLabels := p.labels
	p.labels = nil
	defer func() {
		p.currentFunc = savedFunc
		p.labels = savedLabels
	}()
	env := make(map[string]dependencyCallbackValue)
	for _, name := range dependencyCallbackFieldNames(decl.Type.Params) {
		if name != "" {
			env[name] = dependencyCallbackValue{}
		}
	}
	for name, value := range supplied {
		env[name] = value
	}
	results := make(map[string]bool)
	for _, name := range dependencyCallbackFieldNames(decl.Type.Results) {
		if name != "" {
			results[name] = true
			env[name] = dependencyCallbackValue{}
		}
	}
	p.results = append(p.results, results)
	defer func() { p.results = p.results[:len(p.results)-1] }()
	p.defers = append(p.defers, nil)
	defer func() { p.defers = p.defers[:len(p.defers)-1] }()
	if decl.Recv != nil && len(decl.Recv.List[0].Names) == 1 {
		env[decl.Recv.List[0].Names[0].Name] = receiver
	}
	current := make(map[string]bool, len(env))
	for name := range env {
		current[name] = true
	}
	ok := p.blockWithCurrent(decl.Body, env, depth+1, current) && p.runDeferred(env, depth+1)
	if ok {
		after := dependencyCallbackFrameSnapshot(supplied, receiver)
		if after == before {
			p.done[key] = append(p.done[key], dependencyCallbackCompletedFrame{before: before, after: after})
		}
	}
	return ok
}

func dependencyCallbackNewActiveFrame(supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue) dependencyCallbackActiveFrame {
	// Freeze the entire entry graph with one cloner. Recursive evaluation may
	// mutate the live graph, and receiver/argument aliases must remain visible
	// in the immutable entry facts used by the same-frame check.
	cloner := newDependencyCallbackGraphCloner()
	frozenSupplied := cloner.env(supplied)
	frozenReceiver := cloner.value(receiver)
	return dependencyCallbackActiveFrame{
		supplied:         frozenSupplied,
		receiver:         frozenReceiver,
		argSnapshots:     dependencyCallbackEnvSnapshots(frozenSupplied),
		receiverSnapshot: dependencyCallbackValueSnapshot(frozenReceiver),
	}
}

func dependencyCallbackSameFrame(active dependencyCallbackActiveFrame, supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue) (bool, bool, string) {
	generalized := false
	if dependencyCallbackSameValue(active.receiver, receiver) || dependencyCallbackSameIdentity(active.receiver, receiver) || dependencyCallbackSameLineageGraph(active.receiver, receiver) {
		current := dependencyCallbackValueSnapshot(receiver)
		if current != active.receiverSnapshot {
			if !dependencyCallbackSameCallbackReach(active.receiver, receiver) {
				return false, false, "receiver callback reachability changed after entry"
			}
			dependencyCallbackMarkGeneralized([]dependencyCallbackValue{receiver})
			generalized = true
		} else if receiver.object != nil && !receiver.tainted() {
			dependencyCallbackMarkGeneralized([]dependencyCallbackValue{receiver})
			generalized = true
		}
	} else if active.receiver.tainted() || receiver.tainted() {
		return false, false, "receiver identity changed across tainted state"
	} else {
		dependencyCallbackMarkGeneralized([]dependencyCallbackValue{receiver})
		generalized = true
	}
	if len(active.supplied) != len(supplied) {
		return false, false, "argument count changed"
	}
	for name, value := range active.supplied {
		other, ok := supplied[name]
		if !ok {
			return false, false, "argument " + name + " missing in recursive call"
		}
		if dependencyCallbackSameValue(value, other) || dependencyCallbackSameIdentity(value, other) || dependencyCallbackSameLineageGraph(value, other) {
			current := dependencyCallbackValueSnapshot(other)
			if current != active.argSnapshots[name] {
				if !dependencyCallbackSameCallbackReach(value, other) {
					return false, false, "argument " + name + " callback reachability changed after entry"
				}
				dependencyCallbackMarkGeneralized([]dependencyCallbackValue{other})
				generalized = true
			} else if other.object != nil && !other.tainted() {
				dependencyCallbackMarkGeneralized([]dependencyCallbackValue{other})
				generalized = true
			}
			continue
		}
		if value.tainted() || other.tainted() {
			// A func-typed parameter re-bound to another local closure is a
			// change of code, not of callback position: the new closure holds
			// the callback only where the entry closure already held it, and
			// which literal each one is says nothing about where it lives. What
			// the new code does with it is proved by the generalized body
			// summary below, which walks this body with these actuals.
			if !dependencyCallbackClosureKeepsCallbackPlaces(value, other) {
				return false, false, "argument " + name + " identity changed across tainted state"
			}
		}
		dependencyCallbackMarkGeneralized([]dependencyCallbackValue{other})
		generalized = true
	}
	return true, generalized, ""
}

// dependencyCallbackSameIdentity reports whether two values denote the same
// entity. The active frame holds a frozen clone of the entry graph, so the
// live argument of a recursive call is never pointer-equal to the recorded one
// even when nothing but its contents moved; lineage recovers that identity.
// Identity is a property of the root alone — whether the contents changed is
// the separate question answered by comparing value snapshots, so a mutated
// object stays the same argument instead of looking like a new one.
func dependencyCallbackSameIdentity(left, right dependencyCallbackValue) bool {
	if left.cell != nil || right.cell != nil {
		return left.cell != nil && right.cell != nil &&
			dependencyCallbackCellLineage(left.cell) == dependencyCallbackCellLineage(right.cell)
	}
	if left.callback != nil || right.callback != nil {
		return left.callback != nil && right.callback != nil &&
			dependencyCallbackClosureLineage(left.callback) == dependencyCallbackClosureLineage(right.callback)
	}
	if left.object != nil || right.object != nil {
		return left.object != nil && right.object != nil &&
			dependencyCallbackObjectLineage(left.object) == dependencyCallbackObjectLineage(right.object)
	}
	return false
}

func dependencyCallbackSameValue(left, right dependencyCallbackValue) bool {
	if left.cell != nil || right.cell != nil {
		return left.cell == right.cell && left.escaped == right.escaped
	}
	left = dependencyCallbackResolvedValue(left)
	right = dependencyCallbackResolvedValue(right)
	return left.callback == right.callback && left.object == right.object && left.callable == right.callable && left.escaped == right.escaped
}

type dependencyCallbackGraphPairing struct {
	objectsLR  map[*dependencyCallbackObject]*dependencyCallbackObject
	objectsRL  map[*dependencyCallbackObject]*dependencyCallbackObject
	closuresLR map[*dependencyCallbackClosure]*dependencyCallbackClosure
	closuresRL map[*dependencyCallbackClosure]*dependencyCallbackClosure
	cellsLR    map[*dependencyCallbackCell]*dependencyCallbackCell
	cellsRL    map[*dependencyCallbackCell]*dependencyCallbackCell
	steps      int
}

func dependencyCallbackSameLineageGraph(left, right dependencyCallbackValue) bool {
	pairing := dependencyCallbackGraphPairing{
		objectsLR: make(map[*dependencyCallbackObject]*dependencyCallbackObject), objectsRL: make(map[*dependencyCallbackObject]*dependencyCallbackObject),
		closuresLR: make(map[*dependencyCallbackClosure]*dependencyCallbackClosure), closuresRL: make(map[*dependencyCallbackClosure]*dependencyCallbackClosure),
		cellsLR: make(map[*dependencyCallbackCell]*dependencyCallbackCell), cellsRL: make(map[*dependencyCallbackCell]*dependencyCallbackCell),
	}
	return pairing.value(left, right)
}

func dependencyCallbackObjectLineage(obj *dependencyCallbackObject) *dependencyCallbackObject {
	if obj != nil && obj.lineage != nil {
		return obj.lineage
	}
	return obj
}

func dependencyCallbackClosureLineage(closure *dependencyCallbackClosure) *dependencyCallbackClosure {
	if closure != nil && closure.lineage != nil {
		return closure.lineage
	}
	return closure
}

func dependencyCallbackCellLineage(cell *dependencyCallbackCell) *dependencyCallbackCell {
	if cell != nil && cell.lineage != nil {
		return cell.lineage
	}
	return cell
}

func (p *dependencyCallbackGraphPairing) value(left, right dependencyCallbackValue) bool {
	p.steps++
	if p.steps > 20000 || left.callable != right.callable || left.escaped != right.escaped {
		return false
	}
	if left.cell != nil || right.cell != nil {
		return p.cell(left.cell, right.cell)
	}
	if left.callback != nil || right.callback != nil {
		return p.closure(left.callback, right.callback)
	}
	return p.object(left.object, right.object)
}

func (p *dependencyCallbackGraphPairing) cell(left, right *dependencyCallbackCell) bool {
	if left == nil || right == nil || dependencyCallbackCellLineage(left) != dependencyCallbackCellLineage(right) {
		return left == right
	}
	if paired, ok := p.cellsLR[left]; ok {
		return paired == right
	}
	if paired, ok := p.cellsRL[right]; ok {
		return paired == left
	}
	p.cellsLR[left], p.cellsRL[right] = right, left
	return p.value(left.value, right.value)
}

func (p *dependencyCallbackGraphPairing) closure(left, right *dependencyCallbackClosure) bool {
	if left == nil || right == nil || dependencyCallbackClosureLineage(left) != dependencyCallbackClosureLineage(right) || left.lit != right.lit || len(left.env) != len(right.env) || len(left.captures) != len(right.captures) {
		return left == right
	}
	if paired, ok := p.closuresLR[left]; ok {
		return paired == right
	}
	if paired, ok := p.closuresRL[right]; ok {
		return paired == left
	}
	p.closuresLR[left], p.closuresRL[right] = right, left
	for name, value := range left.env {
		other, ok := right.env[name]
		if !ok || !p.value(value, other) {
			return false
		}
	}
	for name, captured := range left.captures {
		if right.captures[name] != captured {
			return false
		}
	}
	return true
}

func (p *dependencyCallbackGraphPairing) object(left, right *dependencyCallbackObject) bool {
	if left == nil || right == nil || dependencyCallbackObjectLineage(left) != dependencyCallbackObjectLineage(right) || left.typ != right.typ || left.owned != right.owned || left.escaped != right.escaped || left.synthetic != right.synthetic || left.scalar != right.scalar || left.general != right.general {
		return left == right
	}
	if paired, ok := p.objectsLR[left]; ok {
		return paired == right
	}
	if paired, ok := p.objectsRL[right]; ok {
		return paired == left
	}
	p.objectsLR[left], p.objectsRL[right] = right, left
	for name, value := range left.fields {
		if dependencyCallbackSnapshotOmitField(value) {
			if other, ok := right.fields[name]; ok && !dependencyCallbackSnapshotOmitField(other) {
				return false
			}
			continue
		}
		other, ok := right.fields[name]
		if !ok || dependencyCallbackSnapshotOmitField(other) || !p.value(value, other) {
			return false
		}
	}
	for name, value := range right.fields {
		if dependencyCallbackSnapshotOmitField(value) {
			continue
		}
		other, ok := left.fields[name]
		if !ok || dependencyCallbackSnapshotOmitField(other) {
			return false
		}
	}
	return true
}

func (p *dependencyCallbackProof) recursiveBodyStoresCallback(decl *ast.FuncDecl, key string, supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue, depth int) bool {
	if decl == nil || decl.Body == nil {
		return true
	}
	if depth > p.depthLimit {
		return true
	}
	if depth > p.maxDepth {
		p.maxDepth = depth
	}
	frame := key + "\x00" + dependencyCallbackFrameSnapshot(supplied, receiver)
	if stores, ok := p.summaries[frame]; ok {
		return stores
	}
	obligation := key + "\x00" + dependencyCallbackSummaryFrame(supplied, receiver)
	if p.summarizing[obligation] > 0 {
		// Already summarizing this body under this obligation: the enclosing
		// walk covers the same statements with the callback in the same
		// places, so re-walking them here would only re-derive what it already
		// carries, at exponential cost.
		return false
	}
	// The active frame is an immutable entry snapshot used only for the
	// recursion comparison. Build this isolated effect graph from the current
	// recursive actuals, whose aliases and generalized regions describe the
	// invocation being summarized.
	cloner := newDependencyCallbackGraphCloner()
	env := cloner.env(supplied)
	receiver = cloner.value(receiver)
	scope := make(map[string]bool, len(env)+1)
	for name := range env {
		scope[name] = true
	}
	if decl.Recv != nil && len(decl.Recv.List[0].Names) == 1 {
		name := decl.Recv.List[0].Names[0].Name
		env[name] = receiver
		scope[name] = true
	}
	if p.summarizing == nil {
		p.summarizing = make(map[string]int)
	}
	p.summarizing[obligation]++
	stores := p.recursiveBlockStoresCallback(decl.Body, env, scope, scope, depth)
	p.summarizing[obligation]--
	if p.summaries == nil {
		p.summaries = make(map[string]bool)
	}
	p.summaries[frame] = stores
	return stores
}

func (p *dependencyCallbackProof) recursiveBlockStoresCallback(block *ast.BlockStmt, env map[string]dependencyCallbackValue, scope, current map[string]bool, depth int) bool {
	if block == nil {
		return false
	}
	local := dependencyCallbackCloneScope(scope)
	currentLocal := dependencyCallbackCloneScope(current)
	type savedBinding struct {
		value  dependencyCallbackValue
		exists bool
	}
	saved := make(map[string]savedBinding)
	defer func() {
		for name, binding := range saved {
			if binding.exists {
				env[name] = binding.value
			} else {
				delete(env, name)
			}
		}
	}()
	for _, stmt := range block.List {
		p.steps++
		if p.funcSteps == nil {
			p.funcSteps = make(map[string]int)
		}
		p.funcSteps[p.currentFunc]++
		if p.probe != nil {
			p.probe(p)
		}
		if p.steps > p.stepLimit {
			p.refuse(stmt, "proof step bound exceeded")
			return true
		}
		for _, name := range dependencyCallbackStatementDeclarations(stmt) {
			if name == "_" || currentLocal[name] {
				continue
			}
			if _, ok := saved[name]; !ok {
				value, exists := env[name]
				saved[name] = savedBinding{value: value, exists: exists}
			}
		}
		if p.recursiveStatementStoresCallback(stmt, env, local, currentLocal, depth) {
			return true
		}
		for _, name := range dependencyCallbackStatementDeclarations(stmt) {
			if name != "_" {
				local[name] = true
				currentLocal[name] = true
			}
		}
	}
	return false
}

func (p *dependencyCallbackProof) recursiveStatementStoresCallback(stmt ast.Stmt, env map[string]dependencyCallbackValue, scope, current map[string]bool, depth int) bool {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		values := make([]dependencyCallbackValue, len(stmt.Rhs))
		for i, rhs := range stmt.Rhs {
			if !p.expressionCalls(rhs, env, depth) {
				return true
			}
			values[i] = p.value(rhs, env)
		}
		for i, lhs := range stmt.Lhs {
			value := dependencyCallbackValue{}
			if len(values) == len(stmt.Lhs) {
				value = values[i]
			} else if len(values) == 1 {
				value = values[0]
			}
			if stmt.Tok == token.DEFINE && p.recursiveFreshLocalIdent(lhs, current) {
				env[lhs.(*ast.Ident).Name] = value
				continue
			}
			if !p.withStoreContext("assign", lhs, dependencyCallbackExprAt(stmt.Rhs, i), func() bool {
				return p.assign(lhs, value, env, false)
			}) {
				return true
			}
		}
	case *ast.DeclStmt:
		decl, ok := stmt.Decl.(*ast.GenDecl)
		if !ok {
			return false
		}
		for _, item := range decl.Specs {
			spec, ok := item.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range spec.Names {
				value := dependencyCallbackValue{}
				if spec.Type != nil {
					value.object = &dependencyCallbackObject{typ: dependencyCallbackTypeName(spec.Type), owned: true, fields: make(map[string]dependencyCallbackValue)}
				}
				if i < len(spec.Values) {
					if !p.expressionCalls(spec.Values[i], env, depth) {
						return true
					}
					value = p.value(spec.Values[i], env)
					if value.object != nil {
						value.object.owned = true
					}
				}
				value = dependencyCallbackResolvedValue(value)
				if value.object != nil {
					value.object.owned = true
				}
				env[name.Name] = value
			}
		}
	case *ast.ExprStmt:
		if call, ok := stmt.X.(*ast.CallExpr); ok {
			callee := p.value(call.Fun, env)
			if callee.callback != nil && callee.callback.lit == nil || p.recursiveCallTargetsActive(call, env) {
				return false
			}
			if !p.withStoreContext("call", call.Fun, call, func() bool {
				return p.callWithCallee(call, callee, env, depth)
			}) {
				return true
			}
			return false
		}
		if dependencyCallbackNodeTainted(stmt.X, env, p) {
			p.refuse(stmt.X, "expression statement references callback-bearing value in generalized recursive body")
			return true
		}
	case *ast.SendStmt:
		if !p.withStoreContext("send", stmt.Chan, stmt.Value, func() bool {
			return p.statement(stmt, env, depth)
		}) {
			return true
		}
	case *ast.BlockStmt:
		return p.recursiveBlockStoresCallback(stmt, env, dependencyCallbackCloneScope(scope), nil, depth)
	case *ast.IfStmt:
		if stmt.Init != nil && p.recursiveStatementStoresCallback(stmt.Init, env, scope, current, depth) {
			return true
		}
		if p.recursiveBlockStoresCallback(stmt.Body, env, dependencyCallbackCloneScope(scope), nil, depth) {
			return true
		}
		if stmt.Else != nil && p.recursiveStatementStoresCallback(stmt.Else, env, dependencyCallbackCloneScope(scope), nil, depth) {
			return true
		}
	case *ast.ForStmt:
		loop := dependencyCallbackCloneScope(scope)
		loopCurrent := make(map[string]bool)
		if stmt.Init != nil && p.recursiveStatementStoresCallback(stmt.Init, env, loop, loopCurrent, depth) {
			return true
		}
		if p.recursiveBlockStoresCallback(stmt.Body, env, loop, nil, depth) {
			return true
		}
		if stmt.Post != nil && p.recursiveStatementStoresCallback(stmt.Post, env, loop, loopCurrent, depth) {
			return true
		}
	case *ast.RangeStmt:
		return p.recursiveBlockStoresCallback(stmt.Body, env, dependencyCallbackCloneScope(scope), nil, depth)
	case *ast.SwitchStmt:
		next := dependencyCallbackCloneScope(scope)
		nextCurrent := make(map[string]bool)
		if stmt.Init != nil && p.recursiveStatementStoresCallback(stmt.Init, env, next, nextCurrent, depth) {
			return true
		}
		return p.recursiveCaseClausesStoreCallback(stmt.Body, env, next, depth)
	case *ast.TypeSwitchStmt:
		next := dependencyCallbackCloneScope(scope)
		nextCurrent := make(map[string]bool)
		if stmt.Init != nil && p.recursiveStatementStoresCallback(stmt.Init, env, next, nextCurrent, depth) {
			return true
		}
		if stmt.Assign != nil && p.recursiveStatementStoresCallback(stmt.Assign, env, next, nextCurrent, depth) {
			return true
		}
		return p.recursiveCaseClausesStoreCallback(stmt.Body, env, next, depth)
	case *ast.SelectStmt:
		return p.recursiveCommClausesStoreCallback(stmt.Body, env, dependencyCallbackCloneScope(scope), depth)
	case *ast.LabeledStmt:
		return p.recursiveStatementStoresCallback(stmt.Stmt, env, scope, current, depth)
	}
	return false
}

func (p *dependencyCallbackProof) recursiveCallTargetsActive(call *ast.CallExpr, env map[string]dependencyCallbackValue) bool {
	if call == nil {
		return false
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		_, ok := p.active[fun.Name]
		return ok
	case *ast.SelectorExpr:
		receiver := p.value(fun.X, env)
		if receiver.object == nil || receiver.object.typ == "" {
			return false
		}
		_, ok := p.active[receiver.object.typ+"."+fun.Sel.Name]
		return ok
	}
	return false
}

func (p *dependencyCallbackProof) recursiveCaseClausesStoreCallback(body *ast.BlockStmt, env map[string]dependencyCallbackValue, scope map[string]bool, depth int) bool {
	if body == nil {
		return false
	}
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			return true
		}
		next := dependencyCallbackCloneScope(scope)
		if p.recursiveBlockStoresCallback(&ast.BlockStmt{List: clause.Body}, env, next, nil, depth) {
			return true
		}
	}
	return false
}

func (p *dependencyCallbackProof) recursiveCommClausesStoreCallback(body *ast.BlockStmt, env map[string]dependencyCallbackValue, scope map[string]bool, depth int) bool {
	if body == nil {
		return false
	}
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CommClause)
		if !ok {
			return true
		}
		next := dependencyCallbackCloneScope(scope)
		if clause.Comm != nil && p.recursiveStatementStoresCallback(clause.Comm, env, next, make(map[string]bool), depth) {
			return true
		}
		if p.recursiveBlockStoresCallback(&ast.BlockStmt{List: clause.Body}, env, next, nil, depth) {
			return true
		}
	}
	return false
}

func (p *dependencyCallbackProof) recursiveFreshLocalIdent(lhs ast.Expr, current map[string]bool) bool {
	name, ok := lhs.(*ast.Ident)
	if !ok || name.Name == "_" || current[name.Name] || p.globals[name.Name] || p.currentResultName(name.Name) {
		return false
	}
	return true
}

func dependencyCallbackExprAt(exprs []ast.Expr, index int) ast.Expr {
	if len(exprs) == 0 {
		return nil
	}
	if len(exprs) == 1 {
		return exprs[0]
	}
	if index >= 0 && index < len(exprs) {
		return exprs[index]
	}
	return nil
}

func (p *dependencyCallbackProof) withStoreContext(kind string, lhs, rhs ast.Node, fn func() bool) bool {
	oldKind, oldLHS, oldRHS := p.storeKind, p.storeLHS, p.storeRHS
	p.storeKind = kind
	if p.diagnostics {
		p.storeLHS = p.nodeSource(lhs)
		p.storeRHS = p.nodeSource(rhs)
	} else {
		p.storeLHS = ""
		p.storeRHS = ""
	}
	ok := fn()
	p.storeKind, p.storeLHS, p.storeRHS = oldKind, oldLHS, oldRHS
	return ok
}

func (p *dependencyCallbackProof) nodeSource(node ast.Node) string {
	if node == nil {
		return "<none>"
	}
	fset := p.fset
	if fset == nil {
		fset = token.NewFileSet()
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return fmt.Sprintf("%T", node)
	}
	text := strings.Join(strings.Fields(buf.String()), " ")
	if len(text) > 160 {
		text = text[:157] + "..."
	}
	return text
}

func dependencyCallbackCloneScope(scope map[string]bool) map[string]bool {
	clone := make(map[string]bool, len(scope))
	for name, value := range scope {
		clone[name] = value
	}
	return clone
}

func dependencyCallbackFrameSnapshot(supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue) string {
	s := &dependencyCallbackSnapshot{
		objects:  make(map[*dependencyCallbackObject]int),
		closures: make(map[*dependencyCallbackClosure]int),
		cells:    make(map[*dependencyCallbackCell]int),
	}
	return s.frame(supplied, receiver)
}

// dependencyCallbackSummaryFrame identifies the obligation of one generalized
// body summary: the callback-relevant part of the frame, plus the identity of
// every function literal it can reach. A recursive body re-summarized under a
// frame that only grew in a callback-free region -- a lazily allocated map, a
// counter -- carries the obligation the enclosing summary already carries. A
// frame whose func-typed parameter is bound to a different local closure does
// not: other statements run, so it is a summary of its own.
func dependencyCallbackSummaryFrame(supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue) string {
	s := &dependencyCallbackSnapshot{
		objects:   make(map[*dependencyCallbackObject]int),
		closures:  make(map[*dependencyCallbackClosure]int),
		cells:     make(map[*dependencyCallbackCell]int),
		projected: true,
	}
	return s.frame(supplied, receiver)
}

func (s *dependencyCallbackSnapshot) frame(supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue) string {
	var b strings.Builder
	b.WriteString("recv=")
	s.value(&b, receiver)
	b.WriteString(";args=")
	names := make([]string, 0, len(supplied))
	for name := range supplied {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte('=')
		s.value(&b, supplied[name])
		b.WriteByte(';')
	}
	return b.String()
}

func dependencyCallbackEnvSnapshots(env map[string]dependencyCallbackValue) map[string]string {
	snapshots := make(map[string]string, len(env))
	for name, value := range env {
		snapshots[name] = dependencyCallbackValueSnapshot(value)
	}
	return snapshots
}

func dependencyCallbackValueSnapshot(value dependencyCallbackValue) string {
	s := &dependencyCallbackSnapshot{
		objects:  make(map[*dependencyCallbackObject]int),
		closures: make(map[*dependencyCallbackClosure]int),
		cells:    make(map[*dependencyCallbackCell]int),
	}
	var b strings.Builder
	s.value(&b, value)
	return b.String()
}

// dependencyCallbackReachSnapshot renders only the part of a value's graph
// through which the tracked callback is reachable: the callback-free regions
// are elided, so two snapshots are equal exactly when the same callbacks sit
// at the same addresses with the same escape status. Recursion uses it to
// separate "the capture state moved" from "some callback-free field grew".
func dependencyCallbackReachSnapshot(value dependencyCallbackValue) string {
	s := &dependencyCallbackSnapshot{
		objects:   make(map[*dependencyCallbackObject]int),
		closures:  make(map[*dependencyCallbackClosure]int),
		cells:     make(map[*dependencyCallbackCell]int),
		projected: true,
	}
	var b strings.Builder
	s.value(&b, value)
	return b.String()
}

// dependencyCallbackClosureKeepsCallbackPlaces reports whether re-binding a
// func-typed parameter from the local closure a frame was entered with to
// another local closure leaves the callback where it already was: every place
// the new closure holds it is a place the entry closure held it too. Holding it
// in fewer places is admitted -- that only shrinks what this argument could
// retain -- but not in one more. Which literal each closure is, and the
// callback-free graph each navigates to reach those places, are free to differ:
// they are code and shape, not the callback's position. Neither side may be a
// callback with no literal: that is the original callback itself, or the
// unknown value a pointer of unproven origin yields, and neither is a local
// closure over the callback.
func dependencyCallbackClosureKeepsCallbackPlaces(entry, current dependencyCallbackValue) bool {
	entry, current = dependencyCallbackResolvedValue(entry), dependencyCallbackResolvedValue(current)
	if entry.callback == nil || current.callback == nil || entry.callback.lit == nil || current.callback.lit == nil {
		return false
	}
	if entry.escaped != current.escaped || entry.callable != current.callable {
		return false
	}
	entryPlaces, ok := dependencyCallbackCallbackPlaces(entry)
	if !ok {
		return false
	}
	currentPlaces, ok := dependencyCallbackCallbackPlaces(current)
	if !ok {
		return false
	}
	for place := range currentPlaces {
		if !entryPlaces[place] {
			return false
		}
	}
	return true
}

// dependencyCallbackPlaceLimit bounds one place walk, so a cyclic or very wide
// graph costs a refusal rather than the whole proof.
const dependencyCallbackPlaceLimit = 4096

// dependencyCallbackCallbackPlaces collects every place an original callback
// sits in a value's graph: the entity holding it, the name it is held under,
// and whether it has escaped. Holders are identified by lineage, so a frozen
// entry clone names the same place as the live entity it was cloned from, while
// a structurally similar but distinct entity names a different one. How the
// graph is navigated to reach a place is deliberately not part of it: that is
// the callback-free shape of the value, not the callback's position.
func dependencyCallbackCallbackPlaces(value dependencyCallbackValue) (map[string]bool, bool) {
	places := make(map[string]bool)
	objects := make(map[*dependencyCallbackObject]bool)
	closures := make(map[*dependencyCallbackClosure]bool)
	cells := make(map[*dependencyCallbackCell]bool)
	steps := 0
	var walk func(holder, name string, value dependencyCallbackValue) bool
	walk = func(holder, name string, value dependencyCallbackValue) bool {
		steps++
		if steps > dependencyCallbackPlaceLimit {
			return false
		}
		if !value.tainted() {
			return true
		}
		if value.cell != nil {
			cell := value.cell
			if cells[cell] {
				return true
			}
			cells[cell] = true
			return walk(fmt.Sprintf("cell:%p", dependencyCallbackCellLineage(cell)), name, cell.value)
		}
		value = dependencyCallbackResolvedValue(value)
		if value.callback != nil {
			closure := value.callback
			if closure.lit == nil {
				places[fmt.Sprintf("%s.%s:escaped=%t", holder, name, value.escaped)] = true
				return true
			}
			if closures[closure] {
				return true
			}
			closures[closure] = true
			id := fmt.Sprintf("closure:%p", dependencyCallbackClosureLineage(closure))
			for captured, inner := range closure.env {
				if !walk(id, captured, inner) {
					return false
				}
			}
			return true
		}
		if value.object != nil {
			obj := value.object
			if objects[obj] {
				return true
			}
			objects[obj] = true
			id := fmt.Sprintf("object:%p", dependencyCallbackObjectLineage(obj))
			for field, inner := range obj.fields {
				if !walk(id, field, inner) {
					return false
				}
			}
		}
		return true
	}
	if !walk("<root>", "", value) {
		return nil, false
	}
	return places, true
}

// dependencyCallbackValueReachesCallback reports whether an original callback
// -- a closure with no literal -- sits anywhere in a value's graph. It reads
// the answer off the proof's own value graph, with the same projection the
// recursion rule uses, so it is a fact about where callbacks are and never
// about what anything is called. A place walk that exhausts its bound is
// reported as reaching one.
func dependencyCallbackValueReachesCallback(value dependencyCallbackValue) bool {
	if !value.tainted() {
		// tainted() is true of every closure and of every object holding one,
		// so a clean value holds no closure at all, let alone the callback.
		return false
	}
	places, ok := dependencyCallbackCallbackPlaces(value)
	return !ok || len(places) != 0
}

// dependencyCallbackCalleeCannotReachCallback reports that a callee can neither
// invoke nor store an original callback, because no value it can observe
// reaches one.
//
// A Go body observes exactly: its parameters, its receiver, the variables a
// closure captured, the package-level identifiers, and whatever it builds from
// those. Package-level constants and functions are code, not callback-bearing
// state; and no package-level variable can reach a callback in any state this
// proof has not already refused, because every route into one is a refusal --
// a direct store (assign's package global case), a store through a selector,
// index or pointer whose base is the untracked value a global reads as, a
// callback-bearing actual handed to a callee that is not source-visible, and a
// callback-bearing result returned out of the region. So when the actuals, the
// receiver and the callee's own captures all hold no callback, neither the
// callee nor anything it transitively calls can name the callback value, and
// its whole effect on the callback's lifetime is nothing. The caller summarizes
// it by escaping what it was handed -- the callee may have retained those
// clean values, so a later callback store into them must refuse -- and the body
// is not walked at all: no depth is spent and no further steps are charged.
func dependencyCallbackCalleeCannotReachCallback(args []dependencyCallbackValue, receiver, callee dependencyCallbackValue) bool {
	for _, arg := range args {
		if dependencyCallbackValueReachesCallback(arg) {
			return false
		}
	}
	if dependencyCallbackValueReachesCallback(receiver) {
		return false
	}
	return !dependencyCallbackValueReachesCallback(callee)
}

// dependencyCallbackSameCallbackReach reports whether two states put the
// tracked callback in the same places. Differences outside those places are
// callback-free by construction.
func dependencyCallbackSameCallbackReach(left, right dependencyCallbackValue) bool {
	return dependencyCallbackReachSnapshot(left) == dependencyCallbackReachSnapshot(right)
}

type dependencyCallbackSnapshot struct {
	objects  map[*dependencyCallbackObject]int
	closures map[*dependencyCallbackClosure]int
	cells    map[*dependencyCallbackCell]int
	// projected elides every subgraph that cannot reach a callback, and with
	// it the descriptive object flags that only ever widen refusals.
	projected bool
}

func (s *dependencyCallbackSnapshot) value(b *strings.Builder, value dependencyCallbackValue) {
	if s.projected && !value.tainted() {
		b.WriteByte('~')
		return
	}
	if value.cell != nil {
		if id, ok := s.cells[value.cell]; ok {
			fmt.Fprintf(b, "L#%d", id)
			return
		}
		id := len(s.cells) + 1
		s.cells[value.cell] = id
		fmt.Fprintf(b, "L#%d{", id)
		s.value(b, value.cell.value)
		b.WriteByte('}')
		if value.escaped {
			b.WriteString("(escaped)")
		}
		return
	}
	value = dependencyCallbackResolvedValue(value)
	if value.callback != nil {
		s.closure(b, value.callback)
		if value.escaped {
			b.WriteString("(escaped)")
		}
		return
	}
	if value.object != nil {
		s.object(b, value.object)
		if value.escaped {
			b.WriteString("(escaped)")
		}
		return
	}
	if value.callable {
		b.WriteByte('F')
		if value.escaped {
			b.WriteString("(escaped)")
		}
		return
	}
	if value.escaped {
		b.WriteByte('E')
		return
	}
	b.WriteByte('_')
}

func (s *dependencyCallbackSnapshot) closure(b *strings.Builder, closure *dependencyCallbackClosure) {
	if id, ok := s.closures[closure]; ok {
		fmt.Fprintf(b, "C#%d", id)
		return
	}
	id := len(s.closures) + 1
	s.closures[closure] = id
	fmt.Fprintf(b, "C#%d{", id)
	if closure.lit == nil {
		b.WriteString("original")
	} else {
		fmt.Fprintf(b, "lit:%d", closure.lit.Pos())
	}
	names := make([]string, 0, len(closure.env))
	for name := range closure.env {
		if s.projected && !closure.env[name].tainted() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteByte(';')
		b.WriteString(name)
		b.WriteByte('=')
		s.value(b, closure.env[name])
	}
	b.WriteByte('}')
}

func (s *dependencyCallbackSnapshot) object(b *strings.Builder, obj *dependencyCallbackObject) {
	if id, ok := s.objects[obj]; ok {
		fmt.Fprintf(b, "O#%d", id)
		return
	}
	id := len(s.objects) + 1
	s.objects[obj] = id
	if s.projected {
		fmt.Fprintf(b, "O#%d{%s,owned=%t,escaped=%t", id, obj.typ, obj.owned, obj.escaped)
	} else {
		fmt.Fprintf(b, "O#%d{%s,owned=%t,escaped=%t,general=%t", id, obj.typ, obj.owned, obj.escaped, obj.general)
	}
	names := make([]string, 0, len(obj.fields))
	for name := range obj.fields {
		if s.projected && !obj.fields[name].tainted() {
			continue
		}
		if dependencyCallbackSnapshotOmitField(obj.fields[name]) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteByte(';')
		b.WriteString(name)
		b.WriteByte('=')
		s.value(b, obj.fields[name])
	}
	b.WriteByte('}')
}

func dependencyCallbackSnapshotOmitField(value dependencyCallbackValue) bool {
	if value.cell != nil {
		return false
	}
	value = dependencyCallbackResolvedValue(value)
	return value.callback == nil && !value.callable && !value.escaped &&
		value.object != nil && value.object.synthetic && value.object.scalar && len(value.object.fields) == 0
}

type dependencyCallbackDiagnosticGraph struct {
	out      map[string]string
	objects  map[*dependencyCallbackObject]int
	closures map[*dependencyCallbackClosure]int
	seenObj  map[*dependencyCallbackObject]bool
	seenFunc map[*dependencyCallbackClosure]bool
	nodes    int
}

func dependencyCallbackDiagnosticFrame(supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue) map[string]string {
	g := &dependencyCallbackDiagnosticGraph{
		out:      make(map[string]string),
		objects:  make(map[*dependencyCallbackObject]int),
		closures: make(map[*dependencyCallbackClosure]int),
		seenObj:  make(map[*dependencyCallbackObject]bool),
		seenFunc: make(map[*dependencyCallbackClosure]bool),
	}
	g.value("receiver", receiver)
	names := make([]string, 0, len(supplied))
	for name := range supplied {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g.value("arg."+name, supplied[name])
	}
	return g.out
}

func dependencyCallbackCloneDiagnosticFrame(frame map[string]string) map[string]string {
	clone := make(map[string]string, len(frame))
	for path, summary := range frame {
		clone[path] = summary
	}
	return clone
}

func (g *dependencyCallbackDiagnosticGraph) value(path string, value dependencyCallbackValue) {
	value = dependencyCallbackResolvedValue(value)
	if g.nodes >= 512 {
		return
	}
	if dependencyCallbackSnapshotOmitField(value) {
		return
	}
	g.nodes++
	g.out[path] = g.summary(value)
	if value.object != nil {
		if g.seenObj[value.object] {
			return
		}
		g.seenObj[value.object] = true
		names := make([]string, 0, len(value.object.fields))
		for name := range value.object.fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			g.value(path+"."+name, value.object.fields[name])
		}
		return
	}
	if value.callback != nil {
		if g.seenFunc[value.callback] {
			return
		}
		g.seenFunc[value.callback] = true
		names := make([]string, 0, len(value.callback.env))
		for name := range value.callback.env {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			g.value(path+".capture."+name, value.callback.env[name])
		}
	}
}

func (g *dependencyCallbackDiagnosticGraph) summary(value dependencyCallbackValue) string {
	value = dependencyCallbackResolvedValue(value)
	if value.object != nil {
		id := g.objectID(value.object)
		typ := value.object.typ
		if typ == "" {
			typ = "<unknown>"
		}
		return fmt.Sprintf("object#%d{type=%s,owned=%t,escaped=%t,general=%t,synthetic=%t,taint=%t}", id, typ, value.object.owned, value.object.escaped, value.object.general, value.object.synthetic, value.tainted())
	}
	if value.callback != nil {
		id := g.closureID(value.callback)
		kind := "literal"
		if value.callback.lit == nil {
			kind = "original"
		}
		names := make([]string, 0, len(value.callback.env))
		for name := range value.callback.env {
			names = append(names, name)
		}
		sort.Strings(names)
		return fmt.Sprintf("closure#%d{kind=%s,escaped=%t,captures=%s,taint=true}", id, kind, value.escaped, strings.Join(names, ","))
	}
	if value.callable {
		return fmt.Sprintf("callable{escaped=%t,taint=false}", value.escaped)
	}
	if value.escaped {
		return "empty{escaped=true,taint=false}"
	}
	return "empty{taint=false}"
}

func (g *dependencyCallbackDiagnosticGraph) objectID(obj *dependencyCallbackObject) int {
	if id, ok := g.objects[obj]; ok {
		return id
	}
	id := len(g.objects) + 1
	g.objects[obj] = id
	return id
}

func (g *dependencyCallbackDiagnosticGraph) closureID(closure *dependencyCallbackClosure) int {
	if id, ok := g.closures[closure]; ok {
		return id
	}
	id := len(g.closures) + 1
	g.closures[closure] = id
	return id
}

func dependencyCallbackDiagnosticDifferingPaths(left, right map[string]string, limit int) []string {
	seen := make(map[string]bool, len(left)+len(right))
	paths := make([]string, 0, len(left)+len(right))
	for path := range left {
		seen[path] = true
		paths = append(paths, path)
	}
	for path := range right {
		if !seen[path] {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	var diff []string
	for _, path := range paths {
		if left[path] == right[path] {
			continue
		}
		diff = append(diff, path)
		if len(diff) >= limit {
			break
		}
	}
	if len(diff) == 0 {
		diff = append(diff, "<snapshot>")
		left["<snapshot>"] = "<no graph path difference>"
		right["<snapshot>"] = "<no graph path difference>"
	}
	for path := range left {
		if right[path] == "" {
			right[path] = "<missing>"
		}
	}
	for path := range right {
		if left[path] == "" {
			left[path] = "<missing>"
		}
	}
	return diff
}

func (p *dependencyCallbackProof) block(block *ast.BlockStmt, env map[string]dependencyCallbackValue, depth int) bool {
	if block == nil {
		return true
	}
	for _, stmt := range block.List {
		p.steps++
		if p.steps > p.stepLimit {
			return p.refuse(stmt, "proof step bound exceeded")
		}
		if !p.statement(stmt, env, depth) {
			if p.reason == "" {
				p.refuse(stmt, fmt.Sprintf("statement %T refused", stmt))
			}
			return false
		}
	}
	return true
}

// scopedBlock executes a lexical block while preserving assignments to outer
// bindings. Bindings declared by var or := are restored when the block ends.
// Object facts stay monotonic: a nil or clean assignment cannot erase evidence
// that an alias may still carry a callback.
func (p *dependencyCallbackProof) scopedBlock(block *ast.BlockStmt, env map[string]dependencyCallbackValue, depth int) bool {
	return p.scopedBlockWithCurrent(block, env, depth, nil)
}

// blockWithCurrent executes a function body without restoring its bindings;
// deferred calls run after the body and must still be able to resolve locals.
func (p *dependencyCallbackProof) blockWithCurrent(block *ast.BlockStmt, env map[string]dependencyCallbackValue, depth int, current map[string]bool) bool {
	if block == nil {
		return true
	}
	return p.statementList(block.List, env, depth, dependencyCallbackCloneScope(current), false)
}

func (p *dependencyCallbackProof) scopedBlockWithCurrent(block *ast.BlockStmt, env map[string]dependencyCallbackValue, depth int, current map[string]bool) bool {
	if block == nil {
		return true
	}
	return p.statementList(block.List, env, depth, dependencyCallbackCloneScope(current), true)
}

type dependencyCallbackSavedBinding struct {
	value  dependencyCallbackValue
	exists bool
}

// statementList walks one lexical statement list. declared carries the names
// already bound where the list starts, so that := on such a name assigns
// instead of shadowing; scoped restores the bindings the list declares when it
// ends. Labels declared directly by the list are published for the duration of
// the walk so that a goto nested anywhere inside it resolves to an edge.
func (p *dependencyCallbackProof) statementList(list []ast.Stmt, env map[string]dependencyCallbackValue, depth int, declared map[string]bool, scoped bool) bool {
	defer p.registerLabels(list, declared, scoped)()
	var pending []dependencyCallbackLabelCheck
	var saved map[string]dependencyCallbackSavedBinding
	if scoped {
		saved = make(map[string]dependencyCallbackSavedBinding)
		defer func() {
			for name, binding := range saved {
				if binding.exists {
					env[name] = binding.value
				} else {
					delete(env, name)
				}
			}
		}()
	}
	for index, stmt := range list {
		if scoped {
			for _, name := range dependencyCallbackStatementDeclarations(stmt) {
				if name == "_" || declared[name] {
					continue
				}
				value, exists := env[name]
				saved[name] = dependencyCallbackSavedBinding{value: value, exists: exists}
			}
		}
		p.steps++
		if p.funcSteps == nil {
			p.funcSteps = make(map[string]int)
		}
		p.funcSteps[p.currentFunc]++
		if p.probe != nil {
			p.probe(p)
		}
		if p.steps > p.stepLimit {
			return p.refuse(stmt, "proof step bound exceeded")
		}
		if labeled, ok := stmt.(*ast.LabeledStmt); ok {
			if check, backward := p.enterLabel(labeled, list, index, env); backward {
				pending = append(pending, check)
			}
		}
		if !p.statementWithCurrent(stmt, env, depth, declared) {
			if p.reason == "" {
				p.refuse(stmt, fmt.Sprintf("statement %T refused", stmt))
			}
			return false
		}
		for _, name := range dependencyCallbackStatementDeclarations(stmt) {
			if name != "_" {
				declared[name] = true
			}
		}
	}
	for _, check := range pending {
		if dependencyCallbackBranchAddsTaint(check.before, env) {
			return p.refuse(check.labeled, "goto back-edge region may add callback taint")
		}
	}
	return true
}

// registerLabels publishes every label declared directly by a statement list
// and returns the function that takes them back down again.
func (p *dependencyCallbackProof) registerLabels(list []ast.Stmt, declared map[string]bool, scoped bool) func() {
	var restore []func()
	for index, stmt := range list {
		labeled, ok := stmt.(*ast.LabeledStmt)
		if !ok || labeled.Label == nil || labeled.Label.Name == "_" {
			continue
		}
		name := labeled.Label.Name
		if p.labels == nil {
			p.labels = make(map[string]*dependencyCallbackLabel)
		}
		previous, existed := p.labels[name]
		p.labels[name] = &dependencyCallbackLabel{
			list:     list,
			index:    index,
			declared: declared,
			scoped:   scoped,
		}
		restore = append(restore, func() {
			if existed {
				p.labels[name] = previous
			} else {
				delete(p.labels, name)
			}
		})
	}
	if len(restore) == 0 {
		return func() {}
	}
	return func() {
		for _, undo := range restore {
			undo()
		}
	}
}

// enterLabel reaches a label in the statement list that declares it. A label
// that a goto at or after its own position jumps back to is a loop header, and
// the region it heads is the loop body. The back-edge is discharged with the
// same fixpoint condition every other loop in this proof uses: one pass over
// the region may not add callback taint to a binding that was clean where the
// region starts. Because the region is also the straight-line continuation of
// the list, that one pass is the walk already in progress; enterLabel only has
// to record the taint the region starts from.
func (p *dependencyCallbackProof) enterLabel(labeled *ast.LabeledStmt, list []ast.Stmt, index int, env map[string]dependencyCallbackValue) (dependencyCallbackLabelCheck, bool) {
	if labeled.Label == nil {
		return dependencyCallbackLabelCheck{}, false
	}
	label := p.labels[labeled.Label.Name]
	if label == nil || label.entered {
		return dependencyCallbackLabelCheck{}, false
	}
	label.entered = true
	if !dependencyCallbackHasGoto(list[index:], labeled.Label.Name) {
		return dependencyCallbackLabelCheck{}, false
	}
	return dependencyCallbackLabelCheck{labeled: labeled, before: dependencyCallbackTaintSnapshot(env)}, true
}

// gotoEdge models a goto. A jump to a label already entered is a backward
// back-edge whose loop region was modeled where the label was reached, so the
// jump itself carries no new facts. A jump to a label not yet entered is a
// forward edge that skips the statements in between: the suffix the label
// starts is executed over a clone of the env graph as it stands at the jump,
// and, like every other non-linear edge in this proof, it may not add callback
// taint to a binding that was clean.
func (p *dependencyCallbackProof) gotoEdge(stmt *ast.BranchStmt, env map[string]dependencyCallbackValue, depth int) bool {
	if stmt.Label == nil {
		return p.refuse(stmt, "goto without a label is not modeled")
	}
	label := p.labels[stmt.Label.Name]
	if label == nil {
		return p.refuse(stmt, fmt.Sprintf("goto target %s is not declared by an enclosing statement list", stmt.Label.Name))
	}
	if label.entered {
		return true
	}
	before := dependencyCallbackTaintSnapshot(env)
	jump := dependencyCallbackCloneEnvGraph(env)
	if !p.statementList(label.list[label.index:], jump, depth, dependencyCallbackCloneScope(label.declared), label.scoped) {
		return false
	}
	if dependencyCallbackBranchAddsTaint(before, jump) {
		return p.refuse(stmt, "forward goto edge may add callback taint")
	}
	return true
}

// dependencyCallbackHasGoto reports whether any goto targeting name appears in
// the statements. Nested function literals are skipped: Go forbids a goto from
// leaving the function that declares its label.
func dependencyCallbackHasGoto(stmts []ast.Stmt, name string) bool {
	found := false
	visit := func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		switch node := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.BranchStmt:
			if node.Tok == token.GOTO && node.Label != nil && node.Label.Name == name {
				found = true
			}
			return false
		}
		return true
	}
	for _, stmt := range stmts {
		ast.Inspect(stmt, visit)
		if found {
			return true
		}
	}
	return false
}

func (p *dependencyCallbackProof) statementWithCurrent(stmt ast.Stmt, env map[string]dependencyCallbackValue, depth int, current map[string]bool) bool {
	if assign, ok := stmt.(*ast.AssignStmt); ok {
		return p.assignment(assign, env, depth, current)
	}
	if labeled, ok := stmt.(*ast.LabeledStmt); ok {
		return p.statementWithCurrent(labeled.Stmt, env, depth, current)
	}
	return p.statement(stmt, env, depth)
}

func (p *dependencyCallbackProof) assignment(stmt *ast.AssignStmt, env map[string]dependencyCallbackValue, depth int, current map[string]bool) bool {
	values := make([]dependencyCallbackValue, len(stmt.Rhs))
	for i, expr := range stmt.Rhs {
		if !p.expressionCalls(expr, env, depth) {
			return false
		}
		values[i] = p.value(expr, env)
	}
	for i, lhs := range stmt.Lhs {
		value := dependencyCallbackValue{}
		if len(values) == len(stmt.Lhs) {
			value = values[i]
		} else if len(values) == 1 {
			value = values[0]
		}
		if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" && stmt.Tok == token.DEFINE && !current[ident.Name] {
			value = dependencyCallbackResolvedValue(value)
			if value.object != nil {
				value.object.owned = true
			}
			env[ident.Name] = value
			continue
		}
		if !p.assign(lhs, value, env, stmt.Tok == token.DEFINE) {
			return false
		}
	}
	return true
}

func dependencyCallbackStatementDeclarations(stmt ast.Stmt) []string {
	var names []string
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		if stmt.Tok != token.DEFINE {
			return nil
		}
		for _, lhs := range stmt.Lhs {
			if ident, ok := lhs.(*ast.Ident); ok {
				names = append(names, ident.Name)
			}
		}
	case *ast.DeclStmt:
		decl, ok := stmt.Decl.(*ast.GenDecl)
		if !ok {
			return nil
		}
		for _, item := range decl.Specs {
			if spec, ok := item.(*ast.ValueSpec); ok {
				for _, name := range spec.Names {
					names = append(names, name.Name)
				}
			}
		}
	case *ast.LabeledStmt:
		return dependencyCallbackStatementDeclarations(stmt.Stmt)
	}
	return names
}

func dependencyCallbackTaintSnapshot(env map[string]dependencyCallbackValue) map[string]bool {
	snapshot := make(map[string]bool, len(env))
	for name, value := range env {
		snapshot[name] = value.tainted()
	}
	return snapshot
}

func dependencyCallbackBranchAddsTaint(before map[string]bool, after map[string]dependencyCallbackValue) bool {
	for name, tainted := range before {
		if !tainted && after[name].tainted() {
			return true
		}
	}
	return false
}

func (p *dependencyCallbackProof) currentResultTainted(env map[string]dependencyCallbackValue) bool {
	if len(p.results) == 0 {
		return false
	}
	for name := range p.results[len(p.results)-1] {
		if env[name].tainted() {
			return true
		}
	}
	return false
}

func (p *dependencyCallbackProof) statement(stmt ast.Stmt, env map[string]dependencyCallbackValue, depth int) bool {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		return p.assignment(stmt, env, depth, nil)
	case *ast.DeclStmt:
		decl, ok := stmt.Decl.(*ast.GenDecl)
		if !ok {
			return false
		}
		for _, item := range decl.Specs {
			spec, ok := item.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range spec.Names {
				value := dependencyCallbackValue{}
				if spec.Type != nil {
					value.object = &dependencyCallbackObject{typ: dependencyCallbackTypeName(spec.Type), owned: true, fields: make(map[string]dependencyCallbackValue)}
				}
				if i < len(spec.Values) {
					if !p.expressionCalls(spec.Values[i], env, depth) {
						return false
					}
					value = p.value(spec.Values[i], env)
					if value.object != nil {
						value.object.owned = true
					}
				}
				env[name.Name] = value
			}
		}
		return true
	case *ast.ExprStmt:
		call, ok := stmt.X.(*ast.CallExpr)
		return !ok || p.call(call, env, depth)
	case *ast.ReturnStmt:
		if len(stmt.Results) == 0 && p.currentResultTainted(env) {
			return p.refuse(stmt, "naked return exposes tainted named result")
		}
		for _, result := range stmt.Results {
			if !p.expressionCalls(result, env, depth) {
				return false
			}
			value := p.value(result, env)
			if value.tainted() {
				return p.refuse(result, "return exposes callback-bearing value")
			}
			p.markEscaped([]dependencyCallbackValue{value})
		}
		return p.runDeferred(env, depth)
	case *ast.GoStmt:
		if p.callTainted(stmt.Call, env) {
			return p.refuse(stmt, "go statement may invoke callback asynchronously")
		}
		return true
	case *ast.DeferStmt:
		return p.queueDeferredCall(stmt.Call, env, depth)
	case *ast.IfStmt:
		branchBase := dependencyCallbackCloneEnvGraph(env)
		before := dependencyCallbackTaintSnapshot(env)
		if stmt.Init != nil && !p.statement(stmt.Init, branchBase, depth) {
			return p.refuse(stmt.Init, "if initializer refused")
		}
		if !p.expressionCalls(stmt.Cond, branchBase, depth) {
			return false
		}
		if value, ok := p.boolConst(stmt.Cond); ok {
			if value {
				body := dependencyCallbackCloneEnvGraph(branchBase)
				if !p.scopedBlock(stmt.Body, body, depth) || dependencyCallbackBranchAddsTaint(before, body) {
					return p.refuse(stmt.Body, "constant-true if branch may add callback taint")
				}
				return true
			}
			if stmt.Else == nil {
				return true
			}
			other := dependencyCallbackCloneEnvGraph(branchBase)
			if !p.statement(stmt.Else, other, depth) || dependencyCallbackBranchAddsTaint(before, other) {
				return p.refuse(stmt.Else, "constant-false else branch may add callback taint")
			}
			return true
		}
		body := dependencyCallbackCloneEnvGraph(branchBase)
		if !p.scopedBlock(stmt.Body, body, depth) || dependencyCallbackBranchAddsTaint(before, body) {
			return p.refuse(stmt.Body, "if branch may add callback taint")
		}
		if stmt.Else != nil {
			other := dependencyCallbackCloneEnvGraph(branchBase)
			if !p.statement(stmt.Else, other, depth) || dependencyCallbackBranchAddsTaint(before, other) {
				return p.refuse(stmt.Else, "else branch may add callback taint")
			}
		}
		if dependencyCallbackBranchAddsTaint(before, branchBase) {
			return p.refuse(stmt, "if initializer may add callback taint")
		}
		return true
	case *ast.BlockStmt:
		return p.scopedBlock(stmt, env, depth)
	case *ast.ForStmt:
		before := dependencyCallbackTaintSnapshot(env)
		loop := dependencyCallbackCloneEnvGraph(env)
		if stmt.Init != nil && !p.statement(stmt.Init, loop, depth) {
			return p.refuse(stmt.Init, "loop initializer refused")
		}
		if stmt.Cond != nil && !p.expressionCalls(stmt.Cond, loop, depth) {
			return false
		}
		if !p.scopedBlock(stmt.Body, loop, depth) {
			return false
		}
		if stmt.Post != nil && !p.statement(stmt.Post, loop, depth) {
			return false
		}
		if dependencyCallbackBranchAddsTaint(before, loop) {
			return p.refuse(stmt, "loop body may add callback taint")
		}
		return true
	case *ast.RangeStmt:
		if !p.expressionCalls(stmt.X, env, depth) || p.value(stmt.X, env).tainted() {
			return false
		}
		before := dependencyCallbackTaintSnapshot(env)
		loop := dependencyCallbackCloneEnvGraph(env)
		if !p.scopedBlock(stmt.Body, loop, depth) {
			return false
		}
		if dependencyCallbackBranchAddsTaint(before, loop) {
			return p.refuse(stmt, "range body may add callback taint")
		}
		return true
	case *ast.SwitchStmt:
		base := dependencyCallbackCloneEnvGraph(env)
		if stmt.Init != nil && !p.statement(stmt.Init, base, depth) {
			return false
		}
		if stmt.Tag != nil && !p.expressionCalls(stmt.Tag, base, depth) {
			return false
		}
		return p.caseClauses(stmt.Body, base, depth)
	case *ast.TypeSwitchStmt:
		base := dependencyCallbackCloneEnvGraph(env)
		if stmt.Init != nil && !p.statement(stmt.Init, base, depth) {
			return false
		}
		if stmt.Assign != nil && !p.statement(stmt.Assign, base, depth) {
			return false
		}
		return p.caseClauses(stmt.Body, base, depth)
	case *ast.LabeledStmt:
		return p.statement(stmt.Stmt, env, depth)
	case *ast.BranchStmt:
		if stmt.Tok == token.GOTO {
			return p.gotoEdge(stmt, env, depth)
		}
		return true
	case *ast.EmptyStmt, *ast.IncDecStmt:
		return true
	case *ast.SendStmt:
		return p.expressionCalls(stmt.Chan, env, depth) && p.expressionCalls(stmt.Value, env, depth) && !p.value(stmt.Value, env).tainted()
	}
	if dependencyCallbackNodeTainted(stmt, env, p) {
		return p.refuse(stmt, fmt.Sprintf("unsupported statement %T references callback-bearing value", stmt))
	}
	return true
}

func (p *dependencyCallbackProof) boolConst(expr ast.Expr) (bool, bool) {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false, false
	}
	switch ident.Name {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		value, ok := p.consts[ident.Name]
		return value, ok
	}
}

func (p *dependencyCallbackProof) expressionCalls(expr ast.Expr, env map[string]dependencyCallbackValue, depth int) bool {
	ok := true
	ast.Inspect(expr, func(node ast.Node) bool {
		if !ok || node == nil {
			return false
		}
		// A function literal body does not run while the enclosing expression
		// is evaluated, and its statements bind names in the literal's own
		// scope rather than in env: a nested helper's parameter or local that
		// shadows a captured name is a distinct cell. Calls written inside the
		// body are modelled when the closure is invoked, by
		// callWithResolvedCallee, using the closure environment.
		if _, isLit := node.(*ast.FuncLit); isLit {
			return false
		}
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		ok = p.call(call, env, depth)
		return ok
	})
	return ok
}

func (p *dependencyCallbackProof) caseClauses(body *ast.BlockStmt, env map[string]dependencyCallbackValue, depth int) bool {
	before := dependencyCallbackTaintSnapshot(env)
	for _, item := range body.List {
		clause, ok := item.(*ast.CaseClause)
		if !ok {
			return false
		}
		branch := dependencyCallbackCloneEnvGraph(env)
		for _, expr := range clause.List {
			if !p.expressionCalls(expr, branch, depth) {
				return false
			}
		}
		if !p.scopedBlock(&ast.BlockStmt{List: clause.Body}, branch, depth) || dependencyCallbackBranchAddsTaint(before, branch) {
			return p.refuse(clause, "case clause may add callback taint")
		}
	}
	return true
}

func dependencyCallbackCloneEnv(env map[string]dependencyCallbackValue) map[string]dependencyCallbackValue {
	clone := make(map[string]dependencyCallbackValue, len(env))
	for name, value := range env {
		clone[name] = value
	}
	return clone
}

func dependencyCallbackCaptureCell(env map[string]dependencyCallbackValue, name string) dependencyCallbackValue {
	value := env[name]
	if value.cell != nil {
		return value
	}
	cell := &dependencyCallbackCell{value: value}
	captured := dependencyCallbackValue{cell: cell}
	env[name] = captured
	return captured
}

type dependencyCallbackGraphCloner struct {
	objects  map[*dependencyCallbackObject]*dependencyCallbackObject
	closures map[*dependencyCallbackClosure]*dependencyCallbackClosure
	cells    map[*dependencyCallbackCell]*dependencyCallbackCell
}

func newDependencyCallbackGraphCloner() *dependencyCallbackGraphCloner {
	return &dependencyCallbackGraphCloner{
		objects:  make(map[*dependencyCallbackObject]*dependencyCallbackObject),
		closures: make(map[*dependencyCallbackClosure]*dependencyCallbackClosure),
		cells:    make(map[*dependencyCallbackCell]*dependencyCallbackCell),
	}
}

func dependencyCallbackCloneEnvGraph(env map[string]dependencyCallbackValue) map[string]dependencyCallbackValue {
	return newDependencyCallbackGraphCloner().env(env)
}

func (c *dependencyCallbackGraphCloner) env(env map[string]dependencyCallbackValue) map[string]dependencyCallbackValue {
	clone := make(map[string]dependencyCallbackValue, len(env))
	for name, value := range env {
		clone[name] = c.value(value)
	}
	return clone
}

func (c *dependencyCallbackGraphCloner) value(value dependencyCallbackValue) dependencyCallbackValue {
	if value.cell != nil {
		return dependencyCallbackValue{cell: c.cell(value.cell), escaped: value.escaped}
	}
	return dependencyCallbackValue{
		callback: c.closure(value.callback),
		object:   c.object(value.object),
		callable: value.callable,
		escaped:  value.escaped,
	}
}

func (c *dependencyCallbackGraphCloner) cell(cell *dependencyCallbackCell) *dependencyCallbackCell {
	if cell == nil {
		return nil
	}
	if clone := c.cells[cell]; clone != nil {
		return clone
	}
	clone := &dependencyCallbackCell{lineage: dependencyCallbackCellLineage(cell)}
	c.cells[cell] = clone
	clone.value = c.value(cell.value)
	return clone
}

func (c *dependencyCallbackGraphCloner) object(obj *dependencyCallbackObject) *dependencyCallbackObject {
	if obj == nil {
		return nil
	}
	if clone := c.objects[obj]; clone != nil {
		return clone
	}
	clone := &dependencyCallbackObject{
		typ:         obj.typ,
		lineage:     dependencyCallbackObjectLineage(obj),
		owned:       obj.owned,
		escaped:     obj.escaped,
		synthetic:   obj.synthetic,
		scalar:      obj.scalar,
		general:     obj.general,
		boundMethod: obj.boundMethod,
		fields:      make(map[string]dependencyCallbackValue, len(obj.fields)),
	}
	c.objects[obj] = clone
	for name, field := range obj.fields {
		clone.fields[name] = c.value(field)
	}
	return clone
}

func (c *dependencyCallbackGraphCloner) closure(closure *dependencyCallbackClosure) *dependencyCallbackClosure {
	if closure == nil {
		return nil
	}
	if closure.lit == nil {
		return closure
	}
	if clone := c.closures[closure]; clone != nil {
		return clone
	}
	clone := &dependencyCallbackClosure{lit: closure.lit, lineage: dependencyCallbackClosureLineage(closure), captures: dependencyCallbackCloneScope(closure.captures)}
	c.closures[closure] = clone
	clone.env = c.env(closure.env)
	return clone
}

type dependencyCallbackJoinPair struct {
	left  *dependencyCallbackObject
	right *dependencyCallbackObject
}

type dependencyCallbackJoinState struct {
	objects map[dependencyCallbackJoinPair]*dependencyCallbackObject
	steps   int
}

func dependencyCallbackJoinValue(left, right dependencyCallbackValue) dependencyCallbackValue {
	state := dependencyCallbackJoinState{objects: make(map[dependencyCallbackJoinPair]*dependencyCallbackObject)}
	return state.value(left, right)
}

func (s *dependencyCallbackJoinState) value(left, right dependencyCallbackValue) dependencyCallbackValue {
	s.steps++
	if s.steps > 20000 {
		dependencyCallbackMarkEscaped([]dependencyCallbackValue{left, right})
		return dependencyCallbackValue{callback: &dependencyCallbackClosure{}}
	}
	left = dependencyCallbackResolvedValue(left)
	right = dependencyCallbackResolvedValue(right)
	if left.object == nil && left.callback == nil && !left.callable && !left.escaped {
		return right
	}
	if right.object == nil && right.callback == nil && !right.callable && !right.escaped {
		return left
	}
	if left.object != nil && right.object != nil {
		if left.object == right.object {
			return left
		}
		dependencyCallbackMarkEscaped([]dependencyCallbackValue{left, right})
		pair := dependencyCallbackJoinPair{left: left.object, right: right.object}
		if joined := s.objects[pair]; joined != nil {
			return dependencyCallbackValue{object: joined}
		}
		joined := &dependencyCallbackObject{
			typ:     left.object.typ,
			escaped: true,
			general: left.object.general || right.object.general,
			fields:  make(map[string]dependencyCallbackValue),
		}
		s.objects[pair] = joined
		if joined.typ != right.object.typ {
			joined.typ = ""
		}
		s.mergeObjectFields(joined, left.object)
		s.mergeObjectFields(joined, right.object)
		return dependencyCallbackValue{object: joined}
	}
	if right.tainted() {
		dependencyCallbackMarkEscaped([]dependencyCallbackValue{left})
		return right
	}
	if left.tainted() {
		dependencyCallbackMarkEscaped([]dependencyCallbackValue{right})
		return left
	}
	if !right.tainted() {
		dependencyCallbackMarkEscaped([]dependencyCallbackValue{right})
	}
	return left
}

func (s *dependencyCallbackJoinState) mergeObjectFields(dst, src *dependencyCallbackObject) {
	if dst == nil || src == nil {
		return
	}
	for name, value := range src.fields {
		dst.fields[name] = s.value(dst.fields[name], value)
	}
}

func dependencyCallbackOverwriteObject(dst *dependencyCallbackObject, src dependencyCallbackValue) {
	src = dependencyCallbackResolvedValue(src)
	if dst == nil || src.object == nil {
		return
	}
	if dst.typ == "" {
		dst.typ = src.object.typ
	}
	if dst.fields == nil {
		dst.fields = make(map[string]dependencyCallbackValue)
	}
	for name, value := range src.object.fields {
		dst.fields[name] = dependencyCallbackJoinValue(dst.fields[name], value)
	}
}

func dependencyCallbackValueCopy(value dependencyCallbackValue) dependencyCallbackValue {
	value = dependencyCallbackResolvedValue(value)
	if value.object == nil {
		return value
	}
	objects := make(map[*dependencyCallbackObject]*dependencyCallbackObject)
	return dependencyCallbackValue{
		callback: value.callback,
		object:   dependencyCallbackObjectValueCopy(value.object, objects),
		callable: value.callable,
		escaped:  value.escaped,
	}
}

func dependencyCallbackObjectValueCopy(obj *dependencyCallbackObject, seen map[*dependencyCallbackObject]*dependencyCallbackObject) *dependencyCallbackObject {
	if obj == nil {
		return nil
	}
	if copied := seen[obj]; copied != nil {
		return copied
	}
	copied := &dependencyCallbackObject{
		typ:       obj.typ,
		owned:     true,
		escaped:   obj.escaped,
		synthetic: obj.synthetic,
		scalar:    obj.scalar,
		general:   obj.general,
		fields:    make(map[string]dependencyCallbackValue, len(obj.fields)),
	}
	seen[obj] = copied
	for name, field := range obj.fields {
		copied.fields[name] = dependencyCallbackValueCopyField(field)
	}
	return copied
}

func dependencyCallbackValueCopyField(value dependencyCallbackValue) dependencyCallbackValue {
	value = dependencyCallbackResolvedValue(value)
	if value.object == nil {
		return value
	}
	// A dereferenced struct copy gets its own top-level storage, but nested
	// pointer/map/slice-like references remain shared in this proof model.
	return dependencyCallbackValue{
		callback: value.callback,
		object:   value.object,
		callable: value.callable,
		escaped:  value.escaped,
	}
}

func (p *dependencyCallbackProof) assign(lhs ast.Expr, value dependencyCallbackValue, env map[string]dependencyCallbackValue, define bool) bool {
	value = dependencyCallbackResolvedValue(value)
	switch lhs := lhs.(type) {
	case *ast.Ident:
		if lhs.Name == "_" {
			if value.tainted() {
				return p.refuse(lhs, "blank assignment discards callback-bearing value")
			}
			return true
		}
		if p.currentResultName(lhs.Name) && value.tainted() {
			return p.refuse(lhs, "assignment stores callback-bearing value in named result")
		}
		if value.tainted() && dependencyCallbackResolvedValue(env[lhs.Name]).escaped {
			return p.refuse(lhs, "assignment stores callback-bearing value in escaped closure cell")
		}
		if existing := env[lhs.Name]; existing.cell != nil {
			existing.cell.value = value
			return true
		}
		_, local := env[lhs.Name]
		if p.globals[lhs.Name] && !define && !local {
			p.markEscaped([]dependencyCallbackValue{value})
			if value.tainted() {
				return p.refuse(lhs, "assignment stores callback-bearing value in package global")
			}
			return true
		}
		if value.object != nil && define {
			value.object.owned = true
		}
		env[lhs.Name] = value
		return true
	case *ast.SelectorExpr:
		base := p.value(lhs.X, env)
		if !value.tainted() {
			if base.object == nil || !base.object.owned || base.object.escaped {
				p.markEscaped([]dependencyCallbackValue{value})
				return true
			}
			if base.object.general {
				dependencyCallbackMarkGeneralized([]dependencyCallbackValue{value})
			}
			if value.object != nil || value.callable {
				if base.object.fields == nil {
					base.object.fields = make(map[string]dependencyCallbackValue)
				}
				base.object.fields[lhs.Sel.Name] = value
			}
			return true
		}
		if base.object == nil || !base.object.owned || base.object.escaped {
			return p.refuse(lhs, "selector assignment stores callback-bearing value in escaped or unowned object")
		}
		if base.object.general {
			return p.refuse(lhs, "selector assignment stores callback-bearing value in generalized recursive region")
		}
		if base.object.fields == nil {
			base.object.fields = make(map[string]dependencyCallbackValue)
		}
		base.object.fields[lhs.Sel.Name] = value
		return true
	case *ast.IndexExpr:
		p.markEscaped([]dependencyCallbackValue{value})
		if value.tainted() {
			return p.refuse(lhs, "index assignment stores callback-bearing value")
		}
		return true
	case *ast.StarExpr:
		base := p.value(lhs.X, env)
		if base.object == nil || !base.object.owned || base.object.escaped {
			p.markEscaped([]dependencyCallbackValue{value})
		}
		if value.tainted() && (base.object == nil || !base.object.owned || base.object.escaped) {
			return p.refuse(lhs, "pointer assignment stores callback-bearing value through unowned pointer")
		}
		if value.tainted() && base.object != nil && base.object.general {
			return p.refuse(lhs, "pointer assignment stores callback-bearing value in generalized recursive region")
		}
		if base.object != nil && base.object.general {
			dependencyCallbackMarkGeneralized([]dependencyCallbackValue{value})
		}
		if base.object != nil && base.object.owned && !base.object.escaped {
			dependencyCallbackOverwriteObject(base.object, value)
		}
		return true
	}
	if value.tainted() {
		return p.refuse(lhs, "assignment target cannot prove callback-bearing value local")
	}
	return true
}

func (p *dependencyCallbackProof) currentResultName(name string) bool {
	return len(p.results) != 0 && p.results[len(p.results)-1][name]
}

func (p *dependencyCallbackProof) value(expr ast.Expr, env map[string]dependencyCallbackValue) dependencyCallbackValue {
	switch expr := expr.(type) {
	case *ast.Ident:
		return dependencyCallbackResolvedValue(env[expr.Name])
	case *ast.ParenExpr:
		return p.value(expr.X, env)
	case *ast.UnaryExpr:
		return p.value(expr.X, env)
	case *ast.StarExpr:
		base := p.value(expr.X, env)
		if base.object == nil {
			return dependencyCallbackValue{callback: &dependencyCallbackClosure{}, escaped: true}
		}
		return dependencyCallbackValueCopy(base)
	case *ast.IndexExpr:
		container := p.value(expr.X, env)
		if container.object == nil {
			if container.callback != nil || container.callable {
				return container
			}
			return dependencyCallbackValue{}
		}
		if value, ok := container.object.fields[dependencyCallbackElementField]; ok {
			return value
		}
		return dependencyCallbackValue{}
	case *ast.SliceExpr:
		return p.value(expr.X, env)
	case *ast.TypeAssertExpr:
		return p.value(expr.X, env)
	case *ast.SelectorExpr:
		base := p.value(expr.X, env)
		result := p.field(base, expr.Sel.Name, map[string]bool{})
		// A selector that names a method rather than a field builds a method
		// value: it captures the whole receiver, so it can reach every callback
		// the receiver reaches. field only knows about declared and embedded
		// fields, so it reads such a selection as an empty, clean value; when the
		// receiver is callback-bearing that is unsound, because storing the method
		// value retains the callback. Wrap the receiver in a synthetic holder so
		// the method value stays tainted exactly when the receiver is, and record
		// the bound declaration so calling the value proves that body with the
		// captured receiver.
		if !result.tainted() && base.tainted() {
			resolved := dependencyCallbackResolvedValue(base)
			if resolved.object != nil {
				if decl, actual := p.method(resolved, expr.Sel.Name, map[string]bool{}); decl != nil {
					return dependencyCallbackValue{object: &dependencyCallbackObject{
						synthetic:   true,
						boundMethod: decl,
						fields:      map[string]dependencyCallbackValue{dependencyCallbackElementField: actual},
					}}
				}
			}
		}
		return result
	case *ast.FuncLit:
		captures := dependencyCallbackFuncLitCaptures(expr, env)
		closureEnv := make(map[string]dependencyCallbackValue, len(captures))
		for name := range captures {
			closureEnv[name] = dependencyCallbackCaptureCell(env, name)
		}
		return dependencyCallbackValue{callback: &dependencyCallbackClosure{lit: expr, env: closureEnv, captures: captures}}
	case *ast.CompositeLit:
		obj := &dependencyCallbackObject{typ: dependencyCallbackTypeName(expr.Type), owned: true, fields: make(map[string]dependencyCallbackValue)}
		fieldOrder, structLiteral := p.compositeFieldOrder(expr.Type)
		for i, elt := range expr.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				value := p.value(elt, env)
				if structLiteral && i < len(fieldOrder) {
					obj.fields[fieldOrder[i]] = value
					continue
				}
				obj.fields[dependencyCallbackElementField] = dependencyCallbackJoinValue(obj.fields[dependencyCallbackElementField], value)
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if ok && structLiteral {
				obj.fields[key.Name] = p.value(kv.Value, env)
				continue
			}
			obj.fields[dependencyCallbackElementField] = dependencyCallbackJoinValue(obj.fields[dependencyCallbackElementField], p.value(kv.Value, env))
		}
		return dependencyCallbackValue{object: obj}
	case *ast.CallExpr:
		if id, ok := expr.Fun.(*ast.Ident); ok && id.Name == "new" && len(expr.Args) == 1 {
			return dependencyCallbackValue{object: &dependencyCallbackObject{typ: dependencyCallbackTypeName(expr.Args[0]), owned: true, fields: make(map[string]dependencyCallbackValue)}}
		}
	}
	return dependencyCallbackValue{}
}

func (p *dependencyCallbackProof) compositeFieldOrder(expr ast.Expr) ([]string, bool) {
	switch expr := expr.(type) {
	case *ast.Ident:
		shape, ok := p.types[expr.Name]
		return shape.order, ok
	case *ast.StarExpr:
		return p.compositeFieldOrder(expr.X)
	case *ast.StructType:
		var order []string
		for _, field := range expr.Fields.List {
			if len(field.Names) == 0 {
				if name := dependencyCallbackTypeName(field.Type); name != "" {
					order = append(order, name)
				}
				continue
			}
			for _, name := range field.Names {
				order = append(order, name.Name)
			}
		}
		return order, true
	}
	return nil, false
}

func (p *dependencyCallbackProof) callTainted(call *ast.CallExpr, env map[string]dependencyCallbackValue) bool {
	// Inspect the entire invocation, including nested callees such as
	// defer factory(receiver)(). Looking only at the outer call loses the
	// callback-bearing receiver before the deferred closure is formed.
	return dependencyCallbackNodeTainted(call, env, p)
}

// queueDeferredCall records a deferred call for LIFO execution at the enclosing
// function exit. Arguments and call-valued callees are evaluated immediately,
// as Go does; closure bodies are proved later against the final captured state.
func (p *dependencyCallbackProof) queueDeferredCall(call *ast.CallExpr, env map[string]dependencyCallbackValue, depth int) bool {
	if len(p.defers) == 0 {
		return p.refuse(call, "defer outside a tracked function frame")
	}
	deferred := dependencyCallbackDeferred{call: call, args: make([]dependencyCallbackValue, len(call.Args))}
	for i, arg := range call.Args {
		if !p.expressionCalls(arg, env, depth) {
			return false
		}
		deferred.args[i] = p.value(arg, env)
	}
	if factory, ok := call.Fun.(*ast.CallExpr); ok {
		var proved bool
		deferred.callee, proved = p.localSingleResult(factory, env, depth)
		if !proved {
			return false
		}
		deferred.hasCallee = true
	}
	index := len(p.defers) - 1
	p.defers[index] = append(p.defers[index], deferred)
	return true
}

func (p *dependencyCallbackProof) runDeferred(env map[string]dependencyCallbackValue, depth int) bool {
	if len(p.defers) == 0 {
		return true
	}
	index := len(p.defers) - 1
	defers := p.defers[index]
	p.defers[index] = nil
	for i := len(defers) - 1; i >= 0; i-- {
		deferred := defers[i]
		callee := deferred.callee
		if !deferred.hasCallee {
			callee = p.value(deferred.call.Fun, env)
		}
		if !p.callWithResolvedCallee(deferred.call, callee, deferred.args, env, depth) {
			return false
		}
	}
	return true
}

func (p *dependencyCallbackProof) localSingleResult(call *ast.CallExpr, env map[string]dependencyCallbackValue, depth int) (dependencyCallbackValue, bool) {
	args := make([]dependencyCallbackValue, len(call.Args))
	for i, arg := range call.Args {
		if !p.expressionCalls(arg, env, depth) {
			return dependencyCallbackValue{}, false
		}
		args[i] = p.value(arg, env)
	}
	var decl *ast.FuncDecl
	var receiver dependencyCallbackValue
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		decls := p.funcs[fun.Name]
		if len(decls) != 1 {
			return dependencyCallbackValue{}, false
		}
		decl = decls[0]
	case *ast.SelectorExpr:
		receiver = p.value(fun.X, env)
		decl, receiver = p.method(receiver, fun.Sel.Name, map[string]bool{})
		if decl == nil {
			return dependencyCallbackValue{}, false
		}
	default:
		p.refuse(call, "factory call did not resolve to a single local result")
		return dependencyCallbackValue{}, false
	}
	return p.singleResultFunction(call, decl, args, receiver, depth+1)
}

func (p *dependencyCallbackProof) singleResultFunction(call *ast.CallExpr, decl *ast.FuncDecl, args []dependencyCallbackValue, receiver dependencyCallbackValue, depth int) (dependencyCallbackValue, bool) {
	if decl == nil || decl.Body == nil || depth > p.depthLimit || p.steps > p.stepLimit {
		p.refuse(decl, "single-result factory unavailable or proof bounds exceeded")
		return dependencyCallbackValue{}, false
	}
	supplied, bound := dependencyCallbackBindArguments(decl.Type.Params, args)
	if !bound {
		p.refuse(call, fmt.Sprintf("call to %s cannot bind actual=%d to formals=%q", dependencyCallbackFunctionName(decl), len(args), dependencyCallbackFormalLabels(decl.Type.Params)))
		return dependencyCallbackValue{}, false
	}
	if decl.Type.Results == nil || len(decl.Type.Results.List) != 1 || len(decl.Type.Results.List[0].Names) != 0 {
		p.refuse(decl, "deferred factory must have one unnamed result")
		return dependencyCallbackValue{}, false
	}
	list := decl.Body.List
	if len(list) == 0 {
		p.refuse(decl, "deferred factory must end in one return")
		return dependencyCallbackValue{}, false
	}
	ret, ok := list[len(list)-1].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return dependencyCallbackValue{}, false
	}
	for _, stmt := range list[:len(list)-1] {
		if dependencyCallbackContainsReturn(stmt) {
			p.refuse(stmt, "deferred factory has an earlier return")
			return dependencyCallbackValue{}, false
		}
	}
	env := supplied
	if decl.Recv != nil && len(decl.Recv.List[0].Names) == 1 {
		env[decl.Recv.List[0].Names[0].Name] = receiver
	}
	for _, stmt := range list[:len(list)-1] {
		p.steps++
		if p.steps > p.stepLimit {
			p.refuse(stmt, "proof step bound exceeded")
			return dependencyCallbackValue{}, false
		}
		if !p.statement(stmt, env, depth) {
			return dependencyCallbackValue{}, false
		}
	}
	result := ret.Results[0]
	if _, literal := result.(*ast.FuncLit); !literal && !p.expressionCalls(result, env, depth) {
		return dependencyCallbackValue{}, false
	}
	return p.value(result, env), true
}

func dependencyCallbackContainsReturn(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		if found || node == nil {
			return false
		}
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		if _, ok := node.(*ast.ReturnStmt); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func (p *dependencyCallbackProof) call(call *ast.CallExpr, env map[string]dependencyCallbackValue, depth int) bool {
	callee := p.value(call.Fun, env)
	return p.callWithCallee(call, callee, env, depth)
}

func (p *dependencyCallbackProof) callWithCallee(call *ast.CallExpr, callee dependencyCallbackValue, env map[string]dependencyCallbackValue, depth int) bool {
	args := make([]dependencyCallbackValue, len(call.Args))
	for i, arg := range call.Args {
		args[i] = p.value(arg, env)
	}
	return p.callWithResolvedCallee(call, callee, args, env, depth)
}

func (p *dependencyCallbackProof) callWithResolvedCallee(call *ast.CallExpr, callee dependencyCallbackValue, args []dependencyCallbackValue, env map[string]dependencyCallbackValue, depth int) bool {
	if callee.callback != nil {
		if callee.callback.lit == nil {
			return true
		}
		if dependencyCallbackCalleeCannotReachCallback(args, dependencyCallbackValue{}, callee) {
			p.markEscaped(append(append([]dependencyCallbackValue{}, args...), callee))
			return true
		}
		supplied, bound := dependencyCallbackBindArguments(callee.callback.lit.Type.Params, args)
		if !bound {
			return p.refuse(call, fmt.Sprintf("call to function literal cannot bind actual=%d to formals=%q", len(args), dependencyCallbackFormalLabels(callee.callback.lit.Type.Params)))
		}
		closureEnv := dependencyCallbackCloneEnv(callee.callback.env)
		for name, value := range supplied {
			closureEnv[name] = value
		}
		results := make(map[string]bool)
		current := make(map[string]bool, len(supplied))
		for name := range supplied {
			current[name] = true
		}
		for _, name := range dependencyCallbackFieldNames(callee.callback.lit.Type.Results) {
			if name != "" {
				results[name] = true
				current[name] = true
				closureEnv[name] = dependencyCallbackValue{}
			}
		}
		p.results = append(p.results, results)
		defer func() { p.results = p.results[:len(p.results)-1] }()
		p.defers = append(p.defers, nil)
		savedLabels := p.labels
		p.labels = nil
		ok := p.blockWithCurrent(callee.callback.lit.Body, closureEnv, depth+1, current) && p.runDeferred(closureEnv, depth+1)
		p.labels = savedLabels
		p.defers = p.defers[:len(p.defers)-1]
		if ok {
			dependencyCallbackJoinClosureEnvTaint(callee.callback.env, closureEnv, callee.callback.captures)
			dependencyCallbackJoinEnvTaint(env, closureEnv)
		}
		return ok
	}
	// A method value carries its receiver: calling it runs the bound method's
	// body with that receiver, which is what makes the call provable rather
	// than an opaque invocation of a tainted function.
	if bound := dependencyCallbackResolvedValue(callee); bound.object != nil && bound.object.boundMethod != nil {
		return p.callFunction(call, bound.object.boundMethod, args, bound.object.fields[dependencyCallbackElementField], depth)
	}
	tainted := false
	for i := range args {
		tainted = tainted || args[i].tainted()
	}
	// A function-valued field is not a method call: selecting it does not pass
	// its containing object as an implicit receiver. When source declares the
	// field callable and both its current value and explicit arguments are
	// clean, invoking it cannot expose a callback carried elsewhere in the
	// containing object. Tainted fields are represented as callbacks above.
	if callee.callable && !tainted {
		p.markEscaped(args)
		return true
	}
	// Past this point something the callee can observe reaches a callback, so
	// its body must be proved with the actuals it is given. Clean actuals alone
	// would not have made it harmless: a body can materialise an unknown value
	// on its own -- dereferencing a pointer that is not proven to point at a
	// tracked object yields one -- and store it where the callback would be
	// retained. What licenses not descending is the stronger fact checked
	// above, that no route from this call site reaches a callback at all.
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if dependencyCallbackCalleeCannotReachCallback(args, dependencyCallbackValue{}, callee) {
			p.markEscaped(args)
			return true
		}
		if !tainted {
			p.markEscaped(args)
		}
		decls := p.funcs[fun.Name]
		if len(decls) != 1 {
			// A tainted callee value with no resolvable body -- for example a
			// method value whose binding a join erased -- can reach a callback
			// through the receiver it carries, so it refuses like tainted
			// arguments do.
			if tainted || callee.tainted() {
				return p.refuse(fun, "tainted call target is not exactly one local function")
			}
			return true
		}
		return p.callFunction(call, decls[0], args, dependencyCallbackValue{}, depth)
	case *ast.SelectorExpr:
		receiver := p.value(fun.X, env)
		if dependencyCallbackCalleeCannotReachCallback(args, receiver, callee) {
			p.markEscaped(append(append([]dependencyCallbackValue{}, args...), receiver))
			return true
		}
		carriesCallback := tainted || receiver.tainted() || callee.tainted()
		if !carriesCallback {
			p.markEscaped(append(args, receiver))
		}
		decl, actual := p.method(receiver, fun.Sel.Name, map[string]bool{})
		if decl == nil {
			if carriesCallback {
				// The receiver is an interface value with no concrete binding
				// this proof can resolve. Its true dynamic type is one of the
				// source-visible types that declare a method of this name; proving
				// every such method is a sound superset of proving the real one.
				if p.proveInterfaceDispatch(call, fun.Sel.Name, args, depth) {
					return true
				}
				return p.refuse(fun, "tainted method call target is not source-visible")
			}
			return true
		}
		return p.callFunction(call, decl, args, actual, depth)
	}
	if tainted || callee.tainted() {
		return p.refuse(call, "tainted call target is not source-visible")
	}
	return true
}

func (p *dependencyCallbackProof) markEscaped(values []dependencyCallbackValue) {
	dependencyCallbackMarkEscaped(values)
	for _, value := range values {
		p.markClosureCellsEscaped(value.callback, make(map[*dependencyCallbackClosure]bool))
	}
}

func (p *dependencyCallbackProof) markClosureCellsEscaped(closure *dependencyCallbackClosure, seen map[*dependencyCallbackClosure]bool) {
	if closure == nil || seen[closure] {
		return
	}
	seen[closure] = true
	for name, value := range closure.env {
		if value.cell != nil {
			value.cell.value.escaped = true
			p.markClosureCellsEscaped(value.cell.value.callback, seen)
			continue
		}
		value.escaped = true
		closure.env[name] = value
		p.markClosureCellsEscaped(value.callback, seen)
	}
}

func dependencyCallbackMarkEscaped(values []dependencyCallbackValue) {
	seen := make(map[*dependencyCallbackObject]bool)
	for _, value := range values {
		value = dependencyCallbackResolvedValue(value)
		dependencyCallbackMarkObjectEscaped(value.object, seen)
		dependencyCallbackMarkClosureObjectsEscaped(value.callback, seen, make(map[*dependencyCallbackClosure]bool))
	}
}

func dependencyCallbackMarkObjectEscaped(obj *dependencyCallbackObject, seen map[*dependencyCallbackObject]bool) {
	if obj == nil || seen[obj] {
		return
	}
	seen[obj] = true
	obj.escaped = true
	for _, field := range obj.fields {
		field = dependencyCallbackResolvedValue(field)
		dependencyCallbackMarkObjectEscaped(field.object, seen)
	}
}

func dependencyCallbackMarkClosureObjectsEscaped(closure *dependencyCallbackClosure, objects map[*dependencyCallbackObject]bool, closures map[*dependencyCallbackClosure]bool) {
	if closure == nil || closures[closure] {
		return
	}
	closures[closure] = true
	for _, value := range closure.env {
		value = dependencyCallbackResolvedValue(value)
		dependencyCallbackMarkObjectEscaped(value.object, objects)
		dependencyCallbackMarkClosureObjectsEscaped(value.callback, objects, closures)
	}
}

func dependencyCallbackMarkGeneralized(values []dependencyCallbackValue) {
	seen := make(map[*dependencyCallbackObject]bool)
	for _, value := range values {
		value = dependencyCallbackResolvedValue(value)
		dependencyCallbackMarkObjectGeneralized(value.object, seen)
	}
}

func dependencyCallbackMarkObjectGeneralized(obj *dependencyCallbackObject, seen map[*dependencyCallbackObject]bool) {
	if obj == nil || seen[obj] {
		return
	}
	seen[obj] = true
	obj.general = true
	for _, field := range obj.fields {
		field = dependencyCallbackResolvedValue(field)
		dependencyCallbackMarkObjectGeneralized(field.object, seen)
	}
}

func dependencyCallbackSyntheticChild(parent *dependencyCallbackObject, typ string, scalar bool) dependencyCallbackValue {
	child := &dependencyCallbackObject{
		typ:       typ,
		owned:     parent.owned,
		escaped:   parent.escaped,
		synthetic: true,
		scalar:    scalar,
		general:   parent.general,
		fields:    make(map[string]dependencyCallbackValue),
	}
	return dependencyCallbackValue{object: child}
}

func (p *dependencyCallbackProof) callFunction(call *ast.CallExpr, decl *ast.FuncDecl, args []dependencyCallbackValue, receiver dependencyCallbackValue, depth int) bool {
	supplied, bound := dependencyCallbackBindArguments(decl.Type.Params, args)
	if !bound {
		return p.refuse(call, fmt.Sprintf("call to %s cannot bind actual=%d to formals=%q", dependencyCallbackFunctionName(decl), len(args), dependencyCallbackFormalLabels(decl.Type.Params)))
	}
	return p.function(decl, supplied, receiver, depth+1)
}

func dependencyCallbackFunctionName(decl *ast.FuncDecl) string {
	if decl == nil || decl.Name == nil {
		return "<unknown>"
	}
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return decl.Name.Name
	}
	receiver := dependencyCallbackTypeName(decl.Recv.List[0].Type)
	if receiver == "" {
		return decl.Name.Name
	}
	return receiver + "." + decl.Name.Name
}

func (p *dependencyCallbackProof) method(receiver dependencyCallbackValue, name string, seen map[string]bool) (*ast.FuncDecl, dependencyCallbackValue) {
	receiver = dependencyCallbackResolvedValue(receiver)
	if receiver.object == nil || receiver.object.typ == "" || seen[receiver.object.typ] {
		return nil, dependencyCallbackValue{}
	}
	seen[receiver.object.typ] = true
	if decl := p.methods[receiver.object.typ][name]; decl != nil {
		return decl, receiver
	}
	for _, embedded := range p.types[receiver.object.typ].embedded {
		child := receiver.object.fields[embedded]
		if child.object == nil {
			child = dependencyCallbackSyntheticChild(receiver.object, embedded, false)
			receiver.object.fields[embedded] = child
		}
		if decl, actual := p.method(child, name, seen); decl != nil {
			return decl, actual
		}
	}
	return nil, dependencyCallbackValue{}
}

// proveInterfaceDispatch discharges a tainted call whose receiver is an
// interface value the proof cannot bind to a concrete type. The dynamic type at
// runtime is some source-visible type that declares a method of this name;
// which one is not statically known, so the proof stands in for it with every
// candidate. Each candidate is proved against a synthetic receiver of its own
// type -- unowned and escaped, because an interface's dynamic value is not
// storage this call site owns -- so a candidate that retains the callback in a
// field, a global, or an asynchronous launch refuses exactly as a direct call to
// it would. If every candidate leaves the callback where it found it the call is
// admitted; a package with no such method, like any other unresolved tainted
// target, stays refused.
//
// Two gates keep the name-based candidate set from standing in for native
// code. A selector whose base names an imported package is a package-level
// call to code outside the proved sources, so no source method can model it.
// And the candidate set is only evidence when some source-visible interface
// declares the method name: only then is the call an interface method call
// whose dynamic type ranges over source-visible implementations, rather than
// a call on a native value that happens to share a method name with them.
func (p *dependencyCallbackProof) proveInterfaceDispatch(call *ast.CallExpr, name string, args []dependencyCallbackValue, depth int) bool {
	if fun, ok := call.Fun.(*ast.SelectorExpr); ok {
		if base, ok := fun.X.(*ast.Ident); ok && p.imports[base.Name] {
			return false
		}
	}
	if !p.interfaceDeclaresMethod(name) {
		return false
	}
	types := make([]string, 0, len(p.methods))
	for typ, methods := range p.methods {
		if methods[name] != nil {
			types = append(types, typ)
		}
	}
	if len(types) == 0 {
		return false
	}
	sort.Strings(types)
	for _, typ := range types {
		receiver := dependencyCallbackValue{object: &dependencyCallbackObject{
			typ:     typ,
			escaped: true,
			fields:  make(map[string]dependencyCallbackValue),
		}}
		if !p.callFunction(call, p.methods[typ][name], args, receiver, depth) {
			return false
		}
	}
	return true
}

// interfaceDeclaresMethod reports whether any source-visible interface type
// declares a method of this name, directly or through an embedded
// source-visible interface.
func (p *dependencyCallbackProof) interfaceDeclaresMethod(name string) bool {
	for typeName := range p.typeSpecs {
		if p.interfaceTypeDeclares(typeName, name, map[string]bool{}) {
			return true
		}
	}
	return false
}

func (p *dependencyCallbackProof) interfaceTypeDeclares(typeName, method string, seen map[string]bool) bool {
	if seen[typeName] {
		return false
	}
	seen[typeName] = true
	spec := p.typeSpecs[typeName]
	if spec == nil {
		return false
	}
	iface, ok := spec.Type.(*ast.InterfaceType)
	if !ok || iface.Methods == nil {
		return false
	}
	for _, field := range iface.Methods.List {
		if len(field.Names) == 0 {
			if embedded, ok := field.Type.(*ast.Ident); ok && p.interfaceTypeDeclares(embedded.Name, method, seen) {
				return true
			}
			continue
		}
		for _, declared := range field.Names {
			if declared.Name == method {
				return true
			}
		}
	}
	return false
}

// field resolves both declared fields and fields reached through embedding.
// An explicitly selected embedded field, such as p.scanner where scanner is
// anonymous in parser, denotes the embedded object itself. Ownership follows
// the containing object; no package- or type-specific exception is involved.
func (p *dependencyCallbackProof) field(receiver dependencyCallbackValue, name string, seen map[string]bool) dependencyCallbackValue {
	receiver = dependencyCallbackResolvedValue(receiver)
	if receiver.object == nil {
		if receiver.callback != nil || receiver.callable {
			return receiver
		}
		return dependencyCallbackValue{}
	}
	if value, ok := receiver.object.fields[name]; ok {
		return value
	}
	if receiver.object.typ == "" || seen[receiver.object.typ] {
		return dependencyCallbackValue{}
	}
	seen[receiver.object.typ] = true
	shape := p.types[receiver.object.typ]
	if field, ok := shape.fields[name]; ok {
		if field.callable {
			return dependencyCallbackValue{callable: true}
		}
		child := dependencyCallbackSyntheticChild(receiver.object, field.typ, field.scalar)
		receiver.object.fields[name] = child
		return child
	}
	for _, embedded := range shape.embedded {
		child := receiver.object.fields[embedded]
		if child.object == nil {
			child = dependencyCallbackSyntheticChild(receiver.object, embedded, false)
			receiver.object.fields[embedded] = child
		}
		if embedded == name {
			return child
		}
		if value := p.field(child, name, seen); value.object != nil || value.callback != nil {
			return value
		}
	}
	return dependencyCallbackValue{}
}

// dependencyCallbackJoinEnvTaint models the shared lexical cells captured by
// a closure. The proof only needs a monotonic fact: once a deferred closure can
// place a callback-bearing value in an enclosing binding, a later LIFO defer
// must observe that binding as tainted. Clean writes never erase the fact.
func dependencyCallbackJoinEnvTaint(dst, src map[string]dependencyCallbackValue) {
	for _, value := range src {
		if value.cell == nil || !value.tainted() {
			continue
		}
		for _, target := range dst {
			if target.cell == value.cell && !target.tainted() {
				target.cell.value = value.cell.value
			}
		}
	}
}

func dependencyCallbackJoinClosureEnvTaint(dst, src map[string]dependencyCallbackValue, captures map[string]bool) {
	for name := range captures {
		value := src[name]
		target, captured := dst[name]
		if captured && target.cell != nil && target.cell == value.cell && value.tainted() && !target.tainted() {
			target.cell.value = value.cell.value
		}
	}
}

func dependencyCallbackNodeTainted(node ast.Node, env map[string]dependencyCallbackValue, p *dependencyCallbackProof) bool {
	tainted := false
	ast.Inspect(node, func(node ast.Node) bool {
		if tainted || node == nil {
			return false
		}
		expr, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		switch expr.(type) {
		case *ast.Ident, *ast.SelectorExpr:
			if p.value(expr, env).tainted() {
				tainted = true
				return false
			}
		}
		return true
	})
	return tainted
}
