package gosource

import (
	"go/ast"
	"go/types"
)

func (c *converter) localChannelType(t types.Type) bool {
	if c.localChannelTypes == nil {
		c.planLocalChannelTypes()
	}
	return c.localChannelTypes[channelTypeKey(t)]
}

func channelTypeKey(t types.Type) string {
	if t == nil {
		return ""
	}
	if _, ok := t.Underlying().(*types.Chan); !ok {
		return ""
	}
	return types.TypeString(t, func(p *types.Package) string { return p.Path() })
}

// channelTypeKeys finds channels hidden in tuples and aggregates. Native
// results such as *time.Timer therefore carry the provenance of Timer.C.
func channelTypeKeys(t types.Type) []string {
	seen, keys := map[types.Type]bool{}, map[string]bool{}
	var visit func(types.Type)
	visit = func(t types.Type) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		if key := channelTypeKey(t); key != "" {
			keys[key] = true
			return
		}
		switch x := t.Underlying().(type) {
		case *types.Pointer:
			visit(x.Elem())
		case *types.Array:
			visit(x.Elem())
		case *types.Slice:
			visit(x.Elem())
		case *types.Map:
			visit(x.Key())
			visit(x.Elem())
		case *types.Struct:
			for i := 0; i < x.NumFields(); i++ {
				visit(x.Field(i).Type())
			}
		case *types.Tuple:
			for i := 0; i < x.Len(); i++ {
				visit(x.At(i).Type())
			}
		}
	}
	visit(t)
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	return out
}

func (c *converter) planLocalChannelTypes() {
	parent, selected, boundary := map[string]string{}, map[string]bool{}, map[string]bool{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" {
			parent[x] = x
		}
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		if a != "" && b != "" {
			a, b = find(a), find(b)
			if a != b {
				parent[b] = a
			}
		}
	}
	unionTypes := func(a, b types.Type) {
		for _, x := range channelTypeKeys(a) {
			for _, y := range channelTypeKeys(b) {
				union(x, y)
			}
		}
	}
	mark := func(t types.Type) {
		for _, key := range channelTypeKeys(t) {
			boundary[key] = true
		}
	}

	// Imported function values remain imported through arbitrary local aliases.
	nativeFuncs := map[types.Object]bool{}
	var native func(ast.Expr) bool
	native = func(e ast.Expr) bool {
		switch x := ast.Unparen(e).(type) {
		case *ast.Ident:
			o := c.info.ObjectOf(x)
			return nativeFuncs[o] || o != nil && o.Pkg() != nil && o.Pkg().Path() != c.packagePath
		case *ast.SelectorExpr:
			if s := c.info.Selections[x]; s != nil {
				o := s.Obj()
				if o != nil && o.Pkg() != nil && o.Pkg().Path() != c.packagePath {
					return true
				}
				if sig, ok := s.Type().(*types.Signature); ok && sig.Recv() != nil {
					_, ok = sig.Recv().Type().Underlying().(*types.Interface)
					return ok
				}
				return false
			}
			o := c.info.ObjectOf(x.Sel)
			return o != nil && o.Pkg() != nil && o.Pkg().Path() != c.packagePath
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		for _, f := range c.files {
			ast.Inspect(f, func(n ast.Node) bool {
				bind := func(l, r ast.Expr) {
					id, ok := ast.Unparen(l).(*ast.Ident)
					if ok && native(r) {
						if o := c.info.ObjectOf(id); o != nil && !nativeFuncs[o] {
							nativeFuncs[o], changed = true, true
						}
					}
				}
				switch x := n.(type) {
				case *ast.AssignStmt:
					if len(x.Lhs) == len(x.Rhs) {
						for i := range x.Lhs {
							bind(x.Lhs[i], x.Rhs[i])
						}
					}
				case *ast.ValueSpec:
					if len(x.Names) == len(x.Values) {
						for i := range x.Names {
							bind(x.Names[i], x.Values[i])
						}
					}
				}
				return true
			})
		}
	}

	for _, f := range c.files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectStmt:
				var first string
				for _, item := range x.Body.List {
					clause, _ := item.(*ast.CommClause)
					if clause == nil || clause.Comm == nil {
						continue
					}
					var e ast.Expr
					switch y := clause.Comm.(type) {
					case *ast.SendStmt:
						e = y.Chan
					case *ast.ExprStmt:
						if r, ok := ast.Unparen(y.X).(*ast.UnaryExpr); ok {
							e = r.X
						}
					case *ast.AssignStmt:
						if len(y.Rhs) == 1 {
							if r, ok := ast.Unparen(y.Rhs[0]).(*ast.UnaryExpr); ok {
								e = r.X
							}
						}
					}
					key := channelTypeKey(c.info.TypeOf(e))
					if key == "" {
						continue
					}
					selected[key] = true
					if first == "" {
						first = key
					} else {
						union(first, key)
					}
				}
				// Do not prune: native calls in arm bodies invalidate the proof.
			case *ast.CallExpr:
				if native(x.Fun) {
					mark(c.info.TypeOf(x))
					for _, a := range x.Args {
						mark(c.info.TypeOf(a))
					}
				}
			case *ast.AssignStmt:
				for _, l := range x.Lhs {
					for _, r := range x.Rhs {
						unionTypes(c.info.TypeOf(l), c.info.TypeOf(r))
						if t := c.info.TypeOf(l); t != nil {
							if _, ok := t.Underlying().(*types.Interface); ok {
								mark(c.info.TypeOf(r))
							}
						}
					}
				}
			case *ast.ValueSpec:
				for _, n := range x.Names {
					for _, v := range x.Values {
						unionTypes(c.info.TypeOf(n), c.info.TypeOf(v))
						if t := c.info.TypeOf(n); t != nil {
							if _, ok := t.Underlying().(*types.Interface); ok {
								mark(c.info.TypeOf(v))
							}
						}
					}
				}
			case *ast.SendStmt:
				unionTypes(c.info.TypeOf(x.Chan), c.info.TypeOf(x.Value))
			}
			return true
		})
	}
	bad := map[string]bool{}
	for key := range boundary {
		bad[find(key)] = true
	}
	c.localChannelTypes = map[string]bool{}
	for key := range selected {
		if !bad[find(key)] {
			c.localChannelTypes[key] = true
		}
	}
}
