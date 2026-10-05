package gosource

import (
	"go/ast"
	"go/types"
)

// The initial sync proof is package-wide: one uncertain boundary keeps every
// synchronization allocation in the worker. No migration of live state occurs.
func syncTypeName(t types.Type) string {
	if t == nil {
		return ""
	}
	n, ok := types.Unalias(t).(*types.Named)
	if !ok || n.Obj().Pkg() == nil || n.Obj().Pkg().Path() != "sync" {
		return ""
	}
	switch n.Obj().Name() {
	case "Mutex", "RWMutex", "WaitGroup":
		return "sync." + n.Obj().Name()
	}
	return ""
}

func hasSyncType(t types.Type, seen map[types.Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	if syncTypeName(t) != "" {
		return true
	}
	switch x := t.Underlying().(type) {
	case *types.Pointer:
		return hasSyncType(x.Elem(), seen)
	case *types.Array:
		return hasSyncType(x.Elem(), seen)
	case *types.Slice:
		return hasSyncType(x.Elem(), seen)
	case *types.Chan:
		return hasSyncType(x.Elem(), seen)
	case *types.Map:
		return hasSyncType(x.Key(), seen) || hasSyncType(x.Elem(), seen)
	case *types.Struct:
		for i := 0; i < x.NumFields(); i++ {
			if hasSyncType(x.Field(i).Type(), seen) {
				return true
			}
		}
	case *types.Tuple:
		for i := 0; i < x.Len(); i++ {
			if hasSyncType(x.At(i).Type(), seen) {
				return true
			}
		}
	case *types.Signature:
		return hasSyncType(x.Params(), seen) || hasSyncType(x.Results(), seen)
	}
	return false
}

func syncMethod(o types.Object) bool {
	f, ok := o.(*types.Func)
	if !ok {
		return false
	}
	sig := f.Type().(*types.Signature)
	if sig.Recv() == nil {
		return false
	}
	t := sig.Recv().Type()
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	switch syncTypeName(t) {
	case "sync.Mutex":
		return f.Name() == "Lock" || f.Name() == "Unlock" || f.Name() == "TryLock"
	case "sync.RWMutex":
		return f.Name() == "RLock" || f.Name() == "RUnlock" || f.Name() == "Lock" || f.Name() == "Unlock" || f.Name() == "TryLock" || f.Name() == "TryRLock"
	case "sync.WaitGroup":
		return f.Name() == "Add" || f.Name() == "Done" || f.Name() == "Wait"
	}
	return false
}

func (c *converter) localSyncType(t types.Type) string {
	name := syncTypeName(t)
	if name == "" {
		return ""
	}
	if !c.syncPlanned {
		c.planLocalSync()
	}
	if c.syncLocal {
		return name
	}
	return ""
}

func (c *converter) planLocalSync() {
	c.syncPlanned = true
	safe := true
	direct := map[*ast.SelectorExpr]bool{}
	has := func(t types.Type) bool { return hasSyncType(t, map[types.Type]bool{}) }
	// Native descriptors currently preserve identity on value assignment. Keep
	// value-copy programs on the existing path until copy codecs are supported.
	copied := func(e ast.Expr) bool {
		if c.info.Types[e].IsType() {
			return false
		}
		t := c.info.TypeOf(e)
		if t == nil {
			return false
		}
		if _, ok := t.Underlying().(*types.Pointer); ok {
			return false
		}
		if _, ok := ast.Unparen(e).(*ast.CompositeLit); ok {
			return false
		}
		return has(t)
	}
	for _, f := range c.files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if ok {
				if s, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
					if sel := c.info.Selections[s]; sel != nil && sel.Kind() == types.MethodVal && syncMethod(sel.Obj()) {
						direct[s] = true
					}
				}
			}
			return true
		})
	}
	for _, f := range c.files {
		ast.Inspect(f, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok && channelOpaqueStorage(c.info.TypeOf(e), map[types.Type]bool{}) {
				safe = false
			}
			switch x := n.(type) {
			case *ast.BinaryExpr:
				if has(c.info.TypeOf(x.X)) || has(c.info.TypeOf(x.Y)) {
					safe = false
				}
			case *ast.FuncType:
				// Value receivers/parameters/results need copy codecs too.
				for _, fields := range []*ast.FieldList{x.Params, x.Results} {
					if fields != nil {
						for _, f := range fields.List {
							t := c.info.TypeOf(f.Type)
							if t != nil {
								if _, ptr := t.Underlying().(*types.Pointer); !ptr && has(t) {
									safe = false
								}
							}
						}
					}
				}
			case *ast.AssignStmt:
				for _, e := range x.Rhs {
					if copied(e) {
						safe = false
					}
				}
			case *ast.ValueSpec:
				for _, e := range x.Values {
					if copied(e) {
						safe = false
					}
				}
			case *ast.ReturnStmt:
				for _, e := range x.Results {
					if copied(e) {
						safe = false
					}
				}
			case *ast.SendStmt:
				if copied(x.Value) {
					safe = false
				}
			case *ast.CompositeLit:
				for _, e := range x.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						e = kv.Value
					}
					if copied(e) {
						safe = false
					}
				}
			case *ast.RangeStmt:
				if has(c.info.TypeOf(x.X)) {
					safe = false
				}
			case *ast.FuncDecl:
				if x.Recv != nil {
					for _, f := range x.Recv.List {
						t := c.info.TypeOf(f.Type)
						if t != nil {
							if _, ptr := t.Underlying().(*types.Pointer); !ptr && has(t) {
								safe = false
							}
						}
					}
				}
				if x.Body == nil {
					safe = false
				}
			case *ast.Ident:
				o := c.info.ObjectOf(x)
				if o != nil && o.Pkg() != nil && o.Pkg().Path() != c.packagePath {
					if o.Pkg().Path() == "unsafe" {
						safe = false
					}
					if _, ok := o.(*types.TypeName); !ok && !syncMethod(o) && has(o.Type()) {
						safe = false
					}
				}
			case *ast.SelectorExpr:
				if sel := c.info.Selections[x]; sel != nil && syncMethod(sel.Obj()) && !direct[x] {
					safe = false
				}
			case *ast.CallExpr:
				if id, ok := ast.Unparen(x.Fun).(*ast.Ident); ok {
					if builtin, ok := c.info.ObjectOf(id).(*types.Builtin); ok && builtin.Name() != "new" {
						for _, a := range x.Args {
							if has(c.info.TypeOf(a)) {
								safe = false
							}
						}
					}
				}
				for _, a := range x.Args {
					if copied(a) {
						safe = false
					}
				}
				if s, ok := ast.Unparen(x.Fun).(*ast.SelectorExpr); ok && direct[s] {
					break
				}
				if !c.localChannelCall(x.Fun) {
					if has(c.info.TypeOf(x)) {
						safe = false
					}
					if s, ok := ast.Unparen(x.Fun).(*ast.SelectorExpr); ok {
						if has(c.info.TypeOf(s.X)) {
							safe = false
						}
					}
					for _, a := range x.Args {
						if has(c.info.TypeOf(a)) || channelCallbackType(c.info.TypeOf(a), c.packagePath) {
							safe = false
						}
					}
					// Only a statically resolved imported function/method is a
					// known boundary. Indirect calls can expose captured globals.
					known := false
					if sel, ok := ast.Unparen(x.Fun).(*ast.SelectorExpr); ok {
						if selection := c.info.Selections[sel]; selection != nil {
							_, known = selection.Obj().(*types.Func)
						} else {
							_, known = c.info.ObjectOf(sel.Sel).(*types.Func)
						}
					}
					if !known {
						safe = false
					}
				}
			}
			return true
		})
	}
	c.syncLocal = safe
}
