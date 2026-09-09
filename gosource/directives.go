package gosource

import (
	"go/ast"
	"go/token"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Preserve compiler embed directives on the positioned declaration. The native
// build consumes the same relative asset paths as the unchanged source package.
func (c *converter) attachEmbedDirectives(file *syntax.File) {
	byNamePos := map[uint][]syntax.Comment{}
	for _, f := range c.files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				v := spec.(*ast.ValueSpec)
				doc := v.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				if doc == nil || len(v.Names) != 1 {
					continue
				}
				for _, comment := range doc.List {
					if strings.HasPrefix(comment.Text, "//go:embed ") || strings.HasPrefix(comment.Text, "//go:embed\t") {
						pos := c.pos(v.Names[0].Pos()).Offset()
						byNamePos[pos] = append(byNamePos[pos], syntax.Comment{Hash: c.pos(comment.Slash), Text: strings.TrimPrefix(comment.Text, "//")})
					}
				}
			}
		}
	}
	for _, stmt := range file.Stmts {
		if d, ok := stmt.Cmd.(*syntax.BashPPDecl); ok && d.Name != nil {
			stmt.Comments = append(stmt.Comments, byNamePos[d.Name.Pos().Offset()]...)
		}
	}
}
