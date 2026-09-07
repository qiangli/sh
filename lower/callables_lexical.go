package lower

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"
)

func (e *emitter) lexicalKind(name string) string {
	p, ok := e.projections.projectionLookup(name)
	if !ok {
		p = e.projectionType(e.globalTypes[name], nil)
	}
	kind := "KindScalar"
	if p.nativeAggregate {
		kind = "KindObject"
	}
	switch p.kind {
	case projectPointer:
		kind = "KindPointer"
	case projectInterface:
		kind = "KindInterface"
	case projectObject:
		kind = "KindObject"
	}
	return e.prefix + "rt." + kind
}
func (e *emitter) lexicalCell(name, program string) string {
	return e.prefix + "rt.Cell[" + e.globalTypes[name] + "](" + program + ".Bindings," + strconv.Quote("global:"+name) + "," + strconv.Quote(name) + "," + e.lexicalKind(name) + ")"
}
func (e *emitter) lexicalPresence(names []string) string {
	if !e.execution || e.globalTypes == nil {
		return ""
	}
	var out string
	for _, name := range names {
		if name != "_" {
			out += e.lexicalCell(name, e.program()) + ".Present = true\n"
		}
	}
	return out
}
func (e *emitter) lexicalNames(names map[string]bool) string {
	var keys []string
	for name := range names {
		if _, ok := e.globalTypes[name]; ok {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	var pairs []string
	for _, name := range keys {
		pairs = append(pairs, strconv.Quote(name)+":"+strconv.Quote("global:"+name))
	}
	return "map[string]string{" + strings.Join(pairs, ",") + "}"
}

// lexicalStorage uses the native checker's object identity, rather than source
// spelling, to move script storage into each invocation. Shadowed parameters,
// struct fields, named results and loop variables retain ordinary Go storage.
func (e *emitter) lexicalStorage(source []byte, fs *token.FileSet, file *ast.File, pkg *types.Package, info *types.Info) ([]byte, error) {
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	globals := map[types.Object]string{}
	for _, name := range pkg.Scope().Names() {
		if object, ok := pkg.Scope().Lookup(name).(*types.Var); ok {
			if _, known := e.globalTypes[name]; !known {
				e.globalTypes[name] = types.TypeString(object.Type(), func(p *types.Package) string {
					if p == pkg {
						return ""
					}
					return p.Name()
				})
			}
		}
	}
	for name := range e.globalTypes {
		if object, ok := pkg.Scope().Lookup(name).(*types.Var); ok {
			globals[object] = name
		}
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		remove := len(gen.Specs) > 0
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				remove = false
				break
			}
			for _, name := range value.Names {
				if globals[pkg.Scope().Lookup(name.Name)] == "" {
					remove = false
				}
			}
		}
		if remove {
			start, end := fs.Position(gen.Pos()).Offset, fs.Position(gen.End()).Offset
			text := strings.Map(func(r rune) rune {
				if r == '\n' {
					return r
				}
				return ' '
			}, string(source[start:end]))
			edits = append(edits, edit{start, end, text})
		}
	}
	type scope struct {
		end     token.Pos
		program string
	}
	var scopes []scope
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			return true
		}
		for len(scopes) > 0 && node.Pos() >= scopes[len(scopes)-1].end {
			scopes = scopes[:len(scopes)-1]
		}
		var signature *ast.FuncType
		switch n := node.(type) {
		case *ast.FuncDecl:
			signature = n.Type
		case *ast.FuncLit:
			signature = n.Type
		}
		if signature != nil {
			for _, field := range signature.Params.List {
				ptr, ok := field.Type.(*ast.StarExpr)
				if !ok {
					continue
				}
				selector, ok := ptr.X.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Program" {
					continue
				}
				if len(field.Names) == 1 {
					scopes = append(scopes, scope{node.End(), field.Names[0].Name})
					break
				}
			}
		}
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		name := globals[info.Uses[ident]]
		if name == "" {
			return true
		}
		if len(scopes) == 0 {
			return true
		}
		edits = append(edits, edit{fs.Position(ident.Pos()).Offset, fs.Position(ident.End()).Offset, e.lexicalCell(name, scopes[len(scopes)-1].program) + ".Value"})
		return true
	})
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, change := range edits {
		source = append(append(append([]byte{}, source[:change.start]...), []byte(change.text)...), source[change.end:]...)
	}
	out, err := format.Source(source)
	if err != nil {
		return nil, fmt.Errorf("lexical storage: %w", err)
	}
	return out, nil
}
