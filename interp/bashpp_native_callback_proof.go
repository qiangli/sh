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

type dependencyCallbackActiveFrame struct {
	supplied         map[string]dependencyCallbackValue
	receiver         dependencyCallbackValue
	argSnapshots     map[string]string
	receiverSnapshot string
	diagnosticFrame  map[string]string
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
	globals   map[string]bool
	consts    map[string]bool
	active    map[string]dependencyCallbackActiveFrame
	done      map[string][]dependencyCallbackCompletedFrame
	results   []map[string]bool
	defers    [][]dependencyCallbackDeferred
	steps     int

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
	}
	for _, file := range files {
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
	if decl == nil || decl.Body == nil || depth > 64 || p.steps > 20000 {
		return p.refuse(decl, fmt.Sprintf("function unavailable or proof bounds exceeded depth=%d steps=%d", depth, p.steps))
	}
	key := decl.Name.Name
	if decl.Recv != nil {
		key = dependencyCallbackTypeName(decl.Recv.List[0].Type) + "." + key
	}
	if active, ok := p.active[key]; ok {
		// A recursive edge may only preserve callback-bearing state exactly or
		// grow callback-free regions that are then forbidden to receive callbacks.
		if same, generalized, rejection := dependencyCallbackSameFrame(active, supplied, receiver); same {
			if generalized && p.recursiveBodyStoresCallback(decl, active) {
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
	p.active[key] = dependencyCallbackActiveFrame{
		supplied:         dependencyCallbackCloneEnv(supplied),
		receiver:         receiver,
		argSnapshots:     dependencyCallbackEnvSnapshots(supplied),
		receiverSnapshot: dependencyCallbackValueSnapshot(receiver),
	}
	if p.diagnostics {
		active := p.active[key]
		active.diagnosticFrame = dependencyCallbackDiagnosticFrame(supplied, receiver)
		p.active[key] = active
	}
	defer delete(p.active, key)
	savedFunc := p.currentFunc
	p.currentFunc = key
	defer func() { p.currentFunc = savedFunc }()
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
	ok := p.block(decl.Body, env, depth+1) && p.runDeferred(env, depth+1)
	if ok {
		after := dependencyCallbackFrameSnapshot(supplied, receiver)
		if after == before {
			p.done[key] = append(p.done[key], dependencyCallbackCompletedFrame{before: before, after: after})
		}
	}
	return ok
}

func dependencyCallbackSameFrame(active dependencyCallbackActiveFrame, supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue) (bool, bool, string) {
	generalized := false
	if dependencyCallbackSameValue(active.receiver, receiver) || dependencyCallbackSameLineageGraph(active.receiver, receiver) {
		current := dependencyCallbackValueSnapshot(receiver)
		if current != active.receiverSnapshot {
			if active.receiver.tainted() {
				return false, false, "receiver snapshot changed after entry and active receiver is tainted"
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
		dependencyCallbackMarkGeneralized([]dependencyCallbackValue{active.receiver, receiver})
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
		if dependencyCallbackSameValue(value, other) || dependencyCallbackSameLineageGraph(value, other) {
			current := dependencyCallbackValueSnapshot(other)
			if current != active.argSnapshots[name] {
				if value.tainted() {
					return false, false, "argument " + name + " snapshot changed after entry and active argument is tainted"
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
			return false, false, "argument " + name + " identity changed across tainted state"
		}
		dependencyCallbackMarkGeneralized([]dependencyCallbackValue{value, other})
		generalized = true
	}
	return true, generalized, ""
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
	if left == nil || right == nil || dependencyCallbackObjectLineage(left) != dependencyCallbackObjectLineage(right) || left.typ != right.typ || left.owned != right.owned || left.escaped != right.escaped || left.synthetic != right.synthetic || left.scalar != right.scalar || left.general != right.general || len(left.fields) != len(right.fields) {
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
		other, ok := right.fields[name]
		if !ok || !p.value(value, other) {
			return false
		}
	}
	return true
}

func (p *dependencyCallbackProof) recursiveBodyStoresCallback(decl *ast.FuncDecl, active dependencyCallbackActiveFrame) bool {
	if decl == nil || decl.Body == nil {
		return true
	}
	cloner := newDependencyCallbackGraphCloner()
	env := cloner.env(active.supplied)
	receiver := cloner.value(active.receiver)
	scope := make(map[string]bool, len(env)+1)
	for name := range env {
		scope[name] = true
	}
	if decl.Recv != nil && len(decl.Recv.List[0].Names) == 1 {
		name := decl.Recv.List[0].Names[0].Name
		env[name] = receiver
		scope[name] = true
	}
	return p.recursiveBlockStoresCallback(decl.Body, env, scope, scope)
}

func (p *dependencyCallbackProof) recursiveBlockStoresCallback(block *ast.BlockStmt, env map[string]dependencyCallbackValue, scope, current map[string]bool) bool {
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
		for _, name := range dependencyCallbackStatementDeclarations(stmt) {
			if name == "_" || currentLocal[name] {
				continue
			}
			if _, ok := saved[name]; !ok {
				value, exists := env[name]
				saved[name] = savedBinding{value: value, exists: exists}
			}
		}
		if p.recursiveStatementStoresCallback(stmt, env, local, currentLocal) {
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

func (p *dependencyCallbackProof) recursiveStatementStoresCallback(stmt ast.Stmt, env map[string]dependencyCallbackValue, scope, current map[string]bool) bool {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		values := make([]dependencyCallbackValue, len(stmt.Rhs))
		for i, rhs := range stmt.Rhs {
			if !p.expressionCalls(rhs, env, 0) {
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
				return p.assign(lhs, value, env, stmt.Tok == token.DEFINE)
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
					if !p.expressionCalls(spec.Values[i], env, 0) {
						return true
					}
					value = p.value(spec.Values[i], env)
					if value.object != nil {
						value.object.owned = true
					}
				}
				if !p.withStoreContext("decl", name, dependencyCallbackExprAt(spec.Values, i), func() bool {
					return p.assign(name, value, env, true)
				}) {
					return true
				}
			}
		}
	case *ast.ExprStmt:
		if call, ok := stmt.X.(*ast.CallExpr); ok {
			callee := p.value(call.Fun, env)
			if callee.callback != nil && callee.callback.lit == nil || p.recursiveCallTargetsActive(call, env) {
				return false
			}
			if !p.withStoreContext("call", call.Fun, call, func() bool {
				return p.callWithCallee(call, callee, env, 0)
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
			return p.statement(stmt, env, 0)
		}) {
			return true
		}
	case *ast.BlockStmt:
		return p.recursiveBlockStoresCallback(stmt, env, dependencyCallbackCloneScope(scope), nil)
	case *ast.IfStmt:
		if stmt.Init != nil && p.recursiveStatementStoresCallback(stmt.Init, env, scope, current) {
			return true
		}
		if p.recursiveBlockStoresCallback(stmt.Body, env, dependencyCallbackCloneScope(scope), nil) {
			return true
		}
		if stmt.Else != nil && p.recursiveStatementStoresCallback(stmt.Else, env, dependencyCallbackCloneScope(scope), nil) {
			return true
		}
	case *ast.ForStmt:
		loop := dependencyCallbackCloneScope(scope)
		loopCurrent := make(map[string]bool)
		if stmt.Init != nil && p.recursiveStatementStoresCallback(stmt.Init, env, loop, loopCurrent) {
			return true
		}
		if p.recursiveBlockStoresCallback(stmt.Body, env, loop, nil) {
			return true
		}
		if stmt.Post != nil && p.recursiveStatementStoresCallback(stmt.Post, env, loop, loopCurrent) {
			return true
		}
	case *ast.RangeStmt:
		return p.recursiveBlockStoresCallback(stmt.Body, env, dependencyCallbackCloneScope(scope), nil)
	case *ast.SwitchStmt:
		next := dependencyCallbackCloneScope(scope)
		nextCurrent := make(map[string]bool)
		if stmt.Init != nil && p.recursiveStatementStoresCallback(stmt.Init, env, next, nextCurrent) {
			return true
		}
		return p.recursiveCaseClausesStoreCallback(stmt.Body, env, next)
	case *ast.TypeSwitchStmt:
		next := dependencyCallbackCloneScope(scope)
		nextCurrent := make(map[string]bool)
		if stmt.Init != nil && p.recursiveStatementStoresCallback(stmt.Init, env, next, nextCurrent) {
			return true
		}
		if stmt.Assign != nil && p.recursiveStatementStoresCallback(stmt.Assign, env, next, nextCurrent) {
			return true
		}
		return p.recursiveCaseClausesStoreCallback(stmt.Body, env, next)
	case *ast.SelectStmt:
		return p.recursiveCommClausesStoreCallback(stmt.Body, env, dependencyCallbackCloneScope(scope))
	case *ast.LabeledStmt:
		return p.recursiveStatementStoresCallback(stmt.Stmt, env, scope, current)
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

func (p *dependencyCallbackProof) recursiveCaseClausesStoreCallback(body *ast.BlockStmt, env map[string]dependencyCallbackValue, scope map[string]bool) bool {
	if body == nil {
		return false
	}
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			return true
		}
		next := dependencyCallbackCloneScope(scope)
		if p.recursiveBlockStoresCallback(&ast.BlockStmt{List: clause.Body}, env, next, nil) {
			return true
		}
	}
	return false
}

func (p *dependencyCallbackProof) recursiveCommClausesStoreCallback(body *ast.BlockStmt, env map[string]dependencyCallbackValue, scope map[string]bool) bool {
	if body == nil {
		return false
	}
	for _, stmt := range body.List {
		clause, ok := stmt.(*ast.CommClause)
		if !ok {
			return true
		}
		next := dependencyCallbackCloneScope(scope)
		if clause.Comm != nil && p.recursiveStatementStoresCallback(clause.Comm, env, next, make(map[string]bool)) {
			return true
		}
		if p.recursiveBlockStoresCallback(&ast.BlockStmt{List: clause.Body}, env, next, nil) {
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
	}
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
	}
	var b strings.Builder
	s.value(&b, value)
	return b.String()
}

type dependencyCallbackSnapshot struct {
	objects  map[*dependencyCallbackObject]int
	closures map[*dependencyCallbackClosure]int
}

func (s *dependencyCallbackSnapshot) value(b *strings.Builder, value dependencyCallbackValue) {
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
	fmt.Fprintf(b, "O#%d{%s,owned=%t,escaped=%t,general=%t", id, obj.typ, obj.owned, obj.escaped, obj.general)
	names := make([]string, 0, len(obj.fields))
	for name := range obj.fields {
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
		if p.steps > 20000 {
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

func (p *dependencyCallbackProof) scopedBlockWithCurrent(block *ast.BlockStmt, env map[string]dependencyCallbackValue, depth int, current map[string]bool) bool {
	if block == nil {
		return true
	}
	type savedBinding struct {
		value  dependencyCallbackValue
		exists bool
	}
	declared := dependencyCallbackCloneScope(current)
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
		for _, name := range dependencyCallbackStatementDeclarations(stmt) {
			if name == "_" || declared[name] {
				continue
			}
			value, exists := env[name]
			saved[name] = savedBinding{value: value, exists: exists}
			delete(env, name)
			declared[name] = true
		}
		p.steps++
		if p.steps > 20000 {
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
			if !p.assign(lhs, value, env, stmt.Tok == token.DEFINE) {
				return false
			}
		}
		return true
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
		typ:       obj.typ,
		lineage:   dependencyCallbackObjectLineage(obj),
		owned:     obj.owned,
		escaped:   obj.escaped,
		synthetic: obj.synthetic,
		scalar:    obj.scalar,
		general:   obj.general,
		fields:    make(map[string]dependencyCallbackValue, len(obj.fields)),
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
		return p.field(base, expr.Sel.Name, map[string]bool{})
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
	if decl == nil || decl.Body == nil || depth > 64 || p.steps > 20000 {
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
		if p.steps > 20000 {
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
		ok := p.scopedBlockWithCurrent(callee.callback.lit.Body, closureEnv, depth+1, current) && p.runDeferred(closureEnv, depth+1)
		p.defers = p.defers[:len(p.defers)-1]
		if ok {
			dependencyCallbackJoinClosureEnvTaint(callee.callback.env, closureEnv, callee.callback.captures)
			dependencyCallbackJoinEnvTaint(env, closureEnv)
		}
		return ok
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
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if !tainted {
			p.markEscaped(args)
			return true
		}
		decls := p.funcs[fun.Name]
		if len(decls) != 1 {
			return p.refuse(fun, "tainted call target is not exactly one local function")
		}
		return p.callFunction(call, decls[0], args, dependencyCallbackValue{}, depth)
	case *ast.SelectorExpr:
		receiver := p.value(fun.X, env)
		if !tainted && !receiver.tainted() {
			p.markEscaped(append(args, receiver))
			return true
		}
		decl, actual := p.method(receiver, fun.Sel.Name, map[string]bool{})
		if decl == nil {
			return p.refuse(fun, "tainted method call target is not source-visible")
		}
		return p.callFunction(call, decl, args, actual, depth)
	}
	if tainted {
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
