// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPObjectIdentity is shared by the cells created when an object is
// aliased. Marking any root readonly freezes every path through that object.
type bashPPObjectIdentity struct {
	owner    string
	readonly bool
}

type bashPPPathPart struct {
	field string
	index any
	text  string
}

func bashPPWordSource(w *syntax.Word) string {
	var b strings.Builder
	_ = syntax.NewPrinter().Print(&b, w)
	return b.String()
}

// bashPPObjectExpr evaluates only the expression forms in the accepted
// deep-readonly fixtures. It does not attempt to become a general Go evaluator.
func (r *Runner) bashPPObjectExpr(text string) (any, *bashPPObjectIdentity, bool) {
	expr, err := parser.ParseExpr(text)
	if err != nil {
		return nil, nil, false
	}
	if id, ok := expr.(*ast.Ident); ok && r.bashPPScope != nil {
		if cell := r.bashPPScope.lookup(id.Name); cell != nil && cell.vr.Kind == expand.Object {
			return cell.vr.Obj, cell.object, true
		}
	}
	switch expr.(type) {
	case *ast.CompositeLit, *ast.SelectorExpr, *ast.IndexExpr:
	default:
		return nil, nil, false
	}
	value, ok := r.bashPPEvalObjectAST(expr, nil)
	return value, nil, ok
}

func (r *Runner) bashPPEvalObjectAST(expr ast.Expr, expected ast.Expr) (any, bool) {
	switch x := expr.(type) {
	case *ast.BasicLit:
		switch x.Kind {
		case token.STRING:
			v, err := strconv.Unquote(x.Value)
			return v, err == nil
		case token.INT:
			v, err := strconv.Atoi(x.Value)
			return v, err == nil
		}
	case *ast.CompositeLit:
		typ := x.Type
		if typ == nil {
			typ = expected
		}
		switch t := typ.(type) {
		case *ast.MapType:
			out := make(map[string]any, len(x.Elts))
			for _, elt := range x.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					return nil, false
				}
				key, ok := r.bashPPEvalObjectAST(kv.Key, t.Key)
				if !ok {
					return nil, false
				}
				value, ok := r.bashPPEvalObjectAST(kv.Value, t.Value)
				if !ok {
					return nil, false
				}
				out[fmt.Sprint(key)] = value
			}
			return out, true
		case *ast.ArrayType:
			out := make([]any, len(x.Elts))
			for i, elt := range x.Elts {
				value, ok := r.bashPPEvalObjectAST(elt, t.Elt)
				if !ok {
					return nil, false
				}
				out[i] = value
			}
			return out, true
		case *ast.Ident:
			if typ, exists := r.bashPPTypes[t.Name]; !exists || typ.underlying != "struct" {
				return nil, false
			}
			out := make(map[string]any, len(x.Elts))
			for _, elt := range x.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					return nil, false
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					return nil, false
				}
				value, ok := r.bashPPEvalObjectAST(kv.Value, nil)
				if !ok {
					return nil, false
				}
				out[key.Name] = value
			}
			return out, true
		}
	case *ast.Ident:
		if r.bashPPScope != nil {
			if cell := r.bashPPScope.lookup(x.Name); cell != nil && cell.vr.Kind == expand.Object {
				return cell.vr.Obj, true
			}
		}
	case *ast.SelectorExpr, *ast.IndexExpr:
		_, parts, ok := bashPPPath(expr)
		if !ok {
			return nil, false
		}
		root, _, _ := bashPPPath(expr)
		cell := r.bashPPScope.lookup(root)
		if cell == nil || cell.vr.Kind != expand.Object {
			return nil, false
		}
		return bashPPPathValue(cell.vr.Obj, parts)
	}
	return nil, false
}

func bashPPPath(expr ast.Expr) (string, []bashPPPathPart, bool) {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name, nil, true
	case *ast.SelectorExpr:
		root, parts, ok := bashPPPath(x.X)
		return root, append(parts, bashPPPathPart{field: x.Sel.Name, text: "." + x.Sel.Name}), ok
	case *ast.IndexExpr:
		root, parts, ok := bashPPPath(x.X)
		if !ok {
			return "", nil, false
		}
		switch index := x.Index.(type) {
		case *ast.BasicLit:
			if index.Kind == token.STRING {
				v, err := strconv.Unquote(index.Value)
				return root, append(parts, bashPPPathPart{index: v, text: "[" + strconv.Quote(v) + "]"}), err == nil
			}
			if index.Kind == token.INT {
				v, err := strconv.Atoi(index.Value)
				return root, append(parts, bashPPPathPart{index: v, text: "[" + index.Value + "]"}), err == nil
			}
		}
	}
	return "", nil, false
}

func bashPPPathValue(value any, parts []bashPPPathPart) (any, bool) {
	for _, part := range parts {
		switch current := value.(type) {
		case map[string]any:
			key := part.field
			if key == "" {
				key, _ = part.index.(string)
			}
			value = current[key]
		case []any:
			i, ok := part.index.(int)
			if !ok || i < 0 || i >= len(current) {
				return nil, false
			}
			value = current[i]
		default:
			return nil, false
		}
	}
	return value, true
}

func (r *Runner) bashPPAssign(_ context.Context, assign *syntax.BashPPAssign) {
	if !r.objectsEnabled() || r.bashPPScope == nil {
		r.errf("bash++ assignment evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	target := bashPPWordSource(assign.Target)
	expr, err := parser.ParseExpr(target)
	if err != nil {
		r.errf("bash++: unsupported assignment target %s\n", target)
		r.exit = exitStatus{code: 2}
		return
	}
	root, parts, ok := bashPPPath(expr)
	cell := r.bashPPScope.lookup(root)
	if !ok || cell == nil || cell.object == nil || !cell.object.readonly {
		r.errf("bash++: mutation is only implemented for readonly objects\n")
		r.exit = exitStatus{code: 2}
		return
	}
	if len(parts) == 0 {
		r.errf("BASHPP-EREADONLY-MUTATION: cannot assign to readonly value %q\n", cell.object.owner)
		r.exit = exitStatus{code: 2}
		return
	}
	path := ""
	for _, part := range parts {
		path += part.text
	}
	if root != cell.object.owner {
		r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through alias %q and path %s\n",
			cell.object.owner, root, path)
		r.exit = exitStatus{code: 2}
		return
	}
	parent, _ := bashPPPathValue(cell.vr.Obj, parts[:len(parts)-1])
	kind := "field"
	detail := parts[len(parts)-1].text
	switch parent.(type) {
	case map[string]any:
		if parts[len(parts)-1].field == "" {
			kind, detail = "map", path
		}
	case []any:
		kind, detail = "slice", path
	}
	if kind == "field" {
		r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through field %s\n", cell.object.owner, detail)
	} else {
		r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through %s path %s\n", cell.object.owner, kind, detail)
	}
	r.exit = exitStatus{code: 2}
}

func (r *Runner) bashPPResolveWord(w *syntax.Word) (string, bool) {
	if !r.objectsEnabled() || r.bashPPScope == nil {
		return "", false
	}
	expr, err := parser.ParseExpr(bashPPWordSource(w))
	if err != nil {
		return "", false
	}
	root, parts, ok := bashPPPath(expr)
	if !ok || len(parts) == 0 {
		return "", false
	}
	cell := r.bashPPScope.lookup(root)
	if cell == nil || cell.vr.Kind != expand.Object {
		return "", false
	}
	value, ok := bashPPPathValue(cell.vr.Obj, parts)
	if !ok {
		return "", false
	}
	return fmt.Sprint(value), true
}

func (r *Runner) bashPPShortDeclImported(ctx context.Context, d *syntax.BashPPShortDecl) bool {
	if d.Call == nil || len(d.Call.Fun) < 2 {
		return false
	}
	if _, imported := r.bashPPImports[d.Call.Fun[0].Value]; !imported {
		return false
	}
	evaluator, ok := r.bashPPTools.eval.(bashPPValuesEvaluator)
	if !ok {
		r.errf("bash++: selected evaluator cannot return object values\n")
		r.exit = exitStatus{code: 2}
		return true
	}
	req, err := r.bashPPEvalRequest()
	if err == nil {
		req.Results = len(d.Lhs)
		req.Selector = make([]string, len(d.Call.Fun))
		for i, part := range d.Call.Fun {
			req.Selector[i] = part.Value
		}
		req.Args = make([]string, len(d.Call.Args))
		for i, arg := range d.Call.Args {
			req.Args[i] = bashPPWordSource(arg)
		}
		var values []any
		values, err = evaluator.Values(ctx, req)
		if err == nil {
			if len(values) != len(d.Lhs) {
				err = fmt.Errorf("assignment mismatch: %d variable(s) but %d value(s)", len(d.Lhs), len(values))
			} else {
				for i, lhs := range d.Lhs {
					if lhs.Value == "_" {
						continue
					}
					if validateErr := expand.ValidObject(values[i]); validateErr != nil {
						err = validateErr
						break
					}
				}
			}
			if err == nil {
				for i, lhs := range d.Lhs {
					if lhs.Value == "_" {
						continue
					}
					r.bashPPDeclareName(lhs.Value, expand.NewObject(values[i]))
					r.bashPPScope.lookup(lhs.Value).object = &bashPPObjectIdentity{owner: lhs.Value}
				}
				return true
			}
		}
	}
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
	}
	return true
}
