package gosource

import (
	"go/ast"
	"go/token"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// lineDirectives records where the input's own Go line directives change the
// reported filename, one boundary per honored directive. Offsets are relative
// to the file's bytes. The parser already applied every directive to the
// fileset, so each candidate boundary is resolved by asking the token file for
// its adjusted position; a misplaced directive the scanner ignored resolves to
// the previous filename and records nothing. Line and column adjustments reach
// node positions through the fileset; the filename is what a position cannot
// carry.
func lineDirectives(tf *token.File, f *ast.File) []syntax.LineDirective {
	current := tf.Name()
	var out []syntax.LineDirective
	add := func(offset int) {
		if offset < 0 || offset > tf.Size() {
			return
		}
		name := tf.PositionFor(tf.Pos(offset), true).Filename
		if name != current {
			out = append(out, syntax.LineDirective{Offset: uint(offset), Filename: name})
			current = name
		}
	}
	for _, group := range f.Comments {
		for _, cm := range group.List {
			switch {
			case strings.HasPrefix(cm.Text, "//line "), strings.HasPrefix(cm.Text, "//line\t"):
				// A //line directive takes effect at the start of the next line.
				line := tf.PositionFor(cm.End(), false).Line
				if line < tf.LineCount() {
					add(tf.Offset(tf.LineStart(line + 1)))
				}
			case strings.HasPrefix(cm.Text, "/*line "), strings.HasPrefix(cm.Text, "/*line\t"):
				// A /*line*/ directive takes effect at the character following it.
				add(tf.Offset(cm.End()))
			}
		}
	}
	return out
}

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
