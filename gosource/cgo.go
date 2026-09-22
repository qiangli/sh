package gosource

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func cgoPackages(linked []*converter) []syntax.CgoPackage {
	var result []syntax.CgoPackage
	for _, c := range linked {
		var preamble, alias string
		for _, file := range c.files {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.IMPORT {
					continue
				}
				for _, item := range gen.Specs {
					spec := item.(*ast.ImportSpec)
					if strings.Trim(spec.Path.Value, `"`) != "C" {
						continue
					}
					var object types.Object
					if spec.Name != nil {
						object = c.info.Defs[spec.Name]
					} else {
						object = c.info.Implicits[spec]
					}
					if name := c.renames[object]; name != "" {
						alias = name
					} else if object != nil {
						alias = object.Name()
					} else {
						alias = "C"
					}
					doc := spec.Doc
					if doc == nil {
						doc = gen.Doc
					}
					if doc != nil {
						var b strings.Builder
						for _, comment := range doc.List {
							b.WriteString(comment.Text)
							b.WriteByte('\n')
						}
						preamble = b.String()
					}
				}
			}
		}
		if alias == "" {
			continue
		}
		type use struct {
			kind       string
			args       []string
			resultUsed bool
		}
		uses := map[string]use{}
		for _, file := range c.files {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
				if !ok || !cgoSelector(c, sel) {
					return true
				}
				args := make([]string, len(call.Args))
				for i, arg := range call.Args {
					args[i] = cgoProbeArg(c.info.Types[arg].Type)
				}
				uses[sel.Sel.Name] = use{kind: "func", args: args, resultUsed: cgoResultUsed(file, call)}
				return true
			})
			ast.Inspect(file, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if ok && cgoSelector(c, sel) {
					if _, found := uses[sel.Sel.Name]; !found {
						uses[sel.Sel.Name] = use{kind: "unsupported"}
					}
				}
				return true
			})
		}
		names := make([]string, 0, len(uses))
		for name := range uses {
			names = append(names, name)
		}
		sort.Strings(names)
		pkg := syntax.CgoPackage{Path: c.packagePath, Alias: alias, Preamble: preamble}
		for _, name := range names {
			u := uses[name]
			pkg.Symbols = append(pkg.Symbols, syntax.CgoSymbol{Name: name, Kind: u.kind, ProbeArgs: u.args, ResultUsed: u.resultUsed})
		}
		result = append(result, pkg)
	}
	return result
}

func cgoSelector(c *converter, sel *ast.SelectorExpr) bool {
	id, ok := ast.Unparen(sel.X).(*ast.Ident)
	if !ok {
		return false
	}
	pkg, ok := c.info.ObjectOf(id).(*types.PkgName)
	return ok && pkg.Imported().Path() == "C"
}

func cgoProbeArg(typ types.Type) string {
	if typ == nil {
		return "nil"
	}
	switch t := typ.Underlying().(type) {
	case *types.Basic:
		if t.Info()&types.IsBoolean != 0 {
			return "false"
		}
		if t.Info()&(types.IsInteger|types.IsFloat|types.IsComplex) != 0 {
			return "0"
		}
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return "nil"
	}
	return "nil"
}

func cgoResultUsed(file *ast.File, target *ast.CallExpr) bool {
	used := true
	ast.Inspect(file, func(node ast.Node) bool {
		expr, ok := node.(*ast.ExprStmt)
		if ok && ast.Unparen(expr.X) == target {
			used = false
		}
		return true
	})
	return used
}
