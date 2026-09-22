package gosource

import (
	"go/ast"
	"go/types"
)

// localChannelType performs the package-level part of channel allocation.
// Select ties its arm channel types into one allocation domain. A domain is
// local only when it participates in a select and no member is obtained from,
// or passed to, a native package call. This is deliberately type based and
// conservative: an unclassified make keeps the established native policy.
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

func (c *converter) planLocalChannelTypes() {
	parent := map[string]string{}
	selected := map[string]bool{}
	boundary := map[string]bool{}
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
		if a == "" || b == "" {
			return
		}
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	isNativeCall := func(call *ast.CallExpr) bool {
		sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := ast.Unparen(sel.X).(*ast.Ident)
		if !ok {
			return false
		}
		_, ok = c.info.ObjectOf(id).(*types.PkgName)
		return ok
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
					var expr ast.Expr
					switch y := clause.Comm.(type) {
					case *ast.SendStmt:
						expr = y.Chan
					case *ast.ExprStmt:
						if recv, ok := ast.Unparen(y.X).(*ast.UnaryExpr); ok {
							expr = recv.X
						}
					case *ast.AssignStmt:
						if len(y.Rhs) == 1 {
							if recv, ok := ast.Unparen(y.Rhs[0]).(*ast.UnaryExpr); ok {
								expr = recv.X
							}
						}
					}
					if expr == nil {
						continue
					}
					key := channelTypeKey(c.info.TypeOf(expr))
					if key == "" {
						continue
					}
					selected[key] = true
					if first == "" {
						first = key
					} else {
						union(first, key)
					}
					if call, ok := ast.Unparen(expr).(*ast.CallExpr); ok && isNativeCall(call) {
						boundary[key] = true
					}
				}
				return false
			case *ast.CallExpr:
				if isNativeCall(x) {
					key := channelTypeKey(c.info.TypeOf(x))
					if key != "" {
						boundary[key] = true
					}
					for _, arg := range x.Args {
						key := channelTypeKey(c.info.TypeOf(arg))
						if key != "" {
							boundary[key] = true
						}
					}
				}
			}
			return true
		})
	}
	badRoot := map[string]bool{}
	for key := range boundary {
		badRoot[find(key)] = true
	}
	c.localChannelTypes = map[string]bool{}
	for key := range selected {
		if !badRoot[find(key)] {
			c.localChannelTypes[key] = true
		}
	}
}
