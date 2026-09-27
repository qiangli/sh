package interp

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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
	return newDependencyCallbackProof(files).prove(name, callbackArgs)
}

type dependencyCallbackObject struct {
	typ    string
	owned  bool
	escaped bool
	fields map[string]dependencyCallbackValue
}

type dependencyCallbackClosure struct {
	lit *ast.FuncLit
	env map[string]dependencyCallbackValue
}

type dependencyCallbackValue struct {
	callback *dependencyCallbackClosure // nil lit means the original callback
	object   *dependencyCallbackObject
}

func (v dependencyCallbackValue) tainted() bool {
	if v.callback != nil {
		return true
	}
	return dependencyCallbackObjectTainted(v.object, map[*dependencyCallbackObject]bool{})
}

func dependencyCallbackObjectTainted(obj *dependencyCallbackObject, seen map[*dependencyCallbackObject]bool) bool {
	if obj == nil || seen[obj] {
		return false
	}
	seen[obj] = true
	for _, field := range obj.fields {
		if field.callback != nil || dependencyCallbackObjectTainted(field.object, seen) {
			return true
		}
	}
	return false
}

type dependencyCallbackType struct {
	fields   map[string]string
	embedded []string
}

type dependencyCallbackProof struct {
	funcs   map[string][]*ast.FuncDecl
	methods map[string]map[string]*ast.FuncDecl
	types   map[string]dependencyCallbackType
	globals map[string]bool
	active  map[string]bool
	steps   int
}

func newDependencyCallbackProof(files []*ast.File) *dependencyCallbackProof {
	p := &dependencyCallbackProof{
		funcs: make(map[string][]*ast.FuncDecl), methods: make(map[string]map[string]*ast.FuncDecl),
		types: make(map[string]dependencyCallbackType), globals: make(map[string]bool), active: make(map[string]bool),
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
						shape := dependencyCallbackType{fields: make(map[string]string)}
						for _, field := range st.Fields.List {
							fieldType := dependencyCallbackTypeName(field.Type)
							if len(field.Names) == 0 {
								if fieldType != "" {
									shape.embedded = append(shape.embedded, fieldType)
								}
								continue
							}
							for _, name := range field.Names {
								shape.fields[name.Name] = fieldType
							}
						}
						p.types[spec.Name.Name] = shape
					case *ast.ValueSpec:
						for _, name := range spec.Names {
							p.globals[name.Name] = true
						}
					}
				}
			}
		}
	}
	return p
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
		return false
	}
	env := make(map[string]dependencyCallbackValue)
	params := dependencyCallbackFieldNames(decls[0].Type.Params)
	for _, index := range callbackArgs {
		if index < 0 || index >= len(params) || params[index] == "" {
			return false
		}
		env[params[index]] = dependencyCallbackValue{callback: &dependencyCallbackClosure{}}
	}
	return p.function(decls[0], env, dependencyCallbackValue{}, 0)
}

func dependencyCallbackFieldNames(fields *ast.FieldList) []string {
	if fields == nil {
		return nil
	}
	var names []string
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			names = append(names, "")
			continue
		}
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

func (p *dependencyCallbackProof) function(decl *ast.FuncDecl, supplied map[string]dependencyCallbackValue, receiver dependencyCallbackValue, depth int) bool {
	if decl == nil || decl.Body == nil || depth > 64 || p.steps > 20000 {
		return false
	}
	key := decl.Name.Name
	if decl.Recv != nil {
		key = dependencyCallbackTypeName(decl.Recv.List[0].Type) + "." + key
	}
	if p.active[key] {
		return true
	}
	p.active[key] = true
	defer delete(p.active, key)
	env := make(map[string]dependencyCallbackValue)
	for name, value := range supplied {
		env[name] = value
	}
	if decl.Recv != nil && len(decl.Recv.List[0].Names) == 1 {
		env[decl.Recv.List[0].Names[0].Name] = receiver
	}
	return p.block(decl.Body, env, depth+1)
}

func (p *dependencyCallbackProof) block(block *ast.BlockStmt, env map[string]dependencyCallbackValue, depth int) bool {
	if block == nil {
		return true
	}
	for _, stmt := range block.List {
		p.steps++
		if p.steps > 20000 || !p.statement(stmt, env, depth) {
			return false
		}
	}
	return true
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
		for _, result := range stmt.Results {
			if !p.expressionCalls(result, env, depth) {
				return false
			}
			value := p.value(result, env)
			if value.tainted() {
				return false
			}
			dependencyCallbackMarkEscaped([]dependencyCallbackValue{value})
		}
		return true
	case *ast.GoStmt:
		return !p.callTainted(stmt.Call, env)
	case *ast.DeferStmt:
		return !p.callTainted(stmt.Call, env)
	case *ast.IfStmt:
		if stmt.Init != nil && !p.statement(stmt.Init, env, depth) {
			return false
		}
		if !p.expressionCalls(stmt.Cond, env, depth) {
			return false
		}
		if !p.block(stmt.Body, dependencyCallbackCloneEnv(env), depth) {
			return false
		}
		if stmt.Else != nil && !p.statement(stmt.Else, dependencyCallbackCloneEnv(env), depth) {
			return false
		}
		return true
	case *ast.BlockStmt:
		return p.block(stmt, dependencyCallbackCloneEnv(env), depth)
	case *ast.ForStmt:
		if stmt.Init != nil && !p.statement(stmt.Init, env, depth) {
			return false
		}
		if stmt.Post != nil && !p.statement(stmt.Post, env, depth) {
			return false
		}
		if stmt.Cond != nil && !p.expressionCalls(stmt.Cond, env, depth) {
			return false
		}
		return p.block(stmt.Body, dependencyCallbackCloneEnv(env), depth)
	case *ast.RangeStmt:
		if !p.expressionCalls(stmt.X, env, depth) {
			return false
		}
		return p.block(stmt.Body, dependencyCallbackCloneEnv(env), depth)
	case *ast.SwitchStmt:
		if stmt.Init != nil && !p.statement(stmt.Init, env, depth) {
			return false
		}
		return p.caseClauses(stmt.Body, env, depth)
	case *ast.TypeSwitchStmt:
		if stmt.Init != nil && !p.statement(stmt.Init, env, depth) {
			return false
		}
		if stmt.Assign != nil && !p.statement(stmt.Assign, env, depth) {
			return false
		}
		return p.caseClauses(stmt.Body, env, depth)
	case *ast.LabeledStmt:
		return p.statement(stmt.Stmt, env, depth)
	case *ast.BranchStmt, *ast.EmptyStmt, *ast.IncDecStmt:
		return true
	case *ast.SendStmt:
		return p.expressionCalls(stmt.Chan, env, depth) && p.expressionCalls(stmt.Value, env, depth) && !p.value(stmt.Value, env).tainted()
	}
	return !dependencyCallbackNodeTainted(stmt, env, p)
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
	for _, item := range body.List {
		clause, ok := item.(*ast.CaseClause)
		if !ok {
			return false
		}
		branch := dependencyCallbackCloneEnv(env)
		for _, stmt := range clause.Body {
			if !p.statement(stmt, branch, depth) {
				return false
			}
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

func (p *dependencyCallbackProof) assign(lhs ast.Expr, value dependencyCallbackValue, env map[string]dependencyCallbackValue, define bool) bool {
	switch lhs := lhs.(type) {
	case *ast.Ident:
		if lhs.Name == "_" {
			return !value.tainted()
		}
		if p.globals[lhs.Name] && !define {
			dependencyCallbackMarkEscaped([]dependencyCallbackValue{value})
			return !value.tainted()
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
				dependencyCallbackMarkEscaped([]dependencyCallbackValue{value})
			}
			return true
		}
		if base.object == nil || !base.object.owned || base.object.escaped {
			return false
		}
		if base.object.fields == nil {
			base.object.fields = make(map[string]dependencyCallbackValue)
		}
		base.object.fields[lhs.Sel.Name] = value
		return true
	case *ast.IndexExpr:
		dependencyCallbackMarkEscaped([]dependencyCallbackValue{value})
		return !value.tainted()
	case *ast.StarExpr:
		base := p.value(lhs.X, env)
		if base.object == nil || !base.object.owned || base.object.escaped {
			dependencyCallbackMarkEscaped([]dependencyCallbackValue{value})
		}
		return !value.tainted() || base.object != nil && base.object.owned
	}
	return !value.tainted()
}

func (p *dependencyCallbackProof) value(expr ast.Expr, env map[string]dependencyCallbackValue) dependencyCallbackValue {
	switch expr := expr.(type) {
	case *ast.Ident:
		return env[expr.Name]
	case *ast.ParenExpr:
		return p.value(expr.X, env)
	case *ast.UnaryExpr:
		return p.value(expr.X, env)
	case *ast.SelectorExpr:
		base := p.value(expr.X, env)
		if base.object == nil {
			return dependencyCallbackValue{}
		}
		if value, ok := base.object.fields[expr.Sel.Name]; ok {
			return value
		}
		typ := p.types[base.object.typ].fields[expr.Sel.Name]
		child := dependencyCallbackValue{object: &dependencyCallbackObject{typ: typ, owned: base.object.owned, fields: make(map[string]dependencyCallbackValue)}}
		base.object.fields[expr.Sel.Name] = child
		return child
	case *ast.FuncLit:
		if dependencyCallbackNodeTainted(expr.Body, env, p) {
			return dependencyCallbackValue{callback: &dependencyCallbackClosure{lit: expr, env: dependencyCallbackCloneEnv(env)}}
		}
	case *ast.CompositeLit:
		obj := &dependencyCallbackObject{typ: dependencyCallbackTypeName(expr.Type), owned: true, fields: make(map[string]dependencyCallbackValue)}
		for _, elt := range expr.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				value := p.value(elt, env)
				if value.tainted() {
					obj.fields["?"] = value
				}
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if ok {
				obj.fields[key.Name] = p.value(kv.Value, env)
			}
		}
		return dependencyCallbackValue{object: obj}
	case *ast.CallExpr:
		if id, ok := expr.Fun.(*ast.Ident); ok && id.Name == "new" && len(expr.Args) == 1 {
			return dependencyCallbackValue{object: &dependencyCallbackObject{typ: dependencyCallbackTypeName(expr.Args[0]), owned: true, fields: make(map[string]dependencyCallbackValue)}}
		}
	}
	return dependencyCallbackValue{}
}

func (p *dependencyCallbackProof) callTainted(call *ast.CallExpr, env map[string]dependencyCallbackValue) bool {
	if p.value(call.Fun, env).tainted() {
		return true
	}
	for _, arg := range call.Args {
		if p.value(arg, env).tainted() {
			return true
		}
	}
	return false
}

func (p *dependencyCallbackProof) call(call *ast.CallExpr, env map[string]dependencyCallbackValue, depth int) bool {
	callee := p.value(call.Fun, env)
	if callee.callback != nil {
		if callee.callback.lit == nil {
			return true
		}
		return p.block(callee.callback.lit.Body, dependencyCallbackCloneEnv(callee.callback.env), depth+1)
	}
	args := make([]dependencyCallbackValue, len(call.Args))
	tainted := false
	for i, arg := range call.Args {
		args[i] = p.value(arg, env)
		tainted = tainted || args[i].tainted()
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if !tainted {
			dependencyCallbackMarkEscaped(args)
			return true
		}
		decls := p.funcs[fun.Name]
		if len(decls) != 1 {
			return false
		}
		return p.callFunction(decls[0], args, dependencyCallbackValue{}, depth)
	case *ast.SelectorExpr:
		receiver := p.value(fun.X, env)
		if !tainted && !receiver.tainted() {
			dependencyCallbackMarkEscaped(append(args, receiver))
			return true
		}
		decl, actual := p.method(receiver, fun.Sel.Name, map[string]bool{})
		if decl == nil {
			return false
		}
		return p.callFunction(decl, args, actual, depth)
	}
	return !tainted
}

func dependencyCallbackMarkEscaped(values []dependencyCallbackValue) {
	for _, value := range values {
		if value.object != nil {
			value.object.escaped = true
		}
	}
}

func (p *dependencyCallbackProof) callFunction(decl *ast.FuncDecl, args []dependencyCallbackValue, receiver dependencyCallbackValue, depth int) bool {
	names := dependencyCallbackFieldNames(decl.Type.Params)
	if len(names) != len(args) {
		return false
	}
	supplied := make(map[string]dependencyCallbackValue)
	for i, name := range names {
		if name != "" {
			supplied[name] = args[i]
		}
	}
	return p.function(decl, supplied, receiver, depth+1)
}

func (p *dependencyCallbackProof) method(receiver dependencyCallbackValue, name string, seen map[string]bool) (*ast.FuncDecl, dependencyCallbackValue) {
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
			child = dependencyCallbackValue{object: &dependencyCallbackObject{typ: embedded, owned: receiver.object.owned, fields: make(map[string]dependencyCallbackValue)}}
			receiver.object.fields[embedded] = child
		}
		if decl, actual := p.method(child, name, seen); decl != nil {
			return decl, actual
		}
	}
	return nil, dependencyCallbackValue{}
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
