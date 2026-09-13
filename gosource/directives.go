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

// attachEmbedDirectives preserves the compiler directives written on the
// input's declarations — //go:embed on a var; //go:noinline, //go:nosplit,
// //go:norace, //go:noescape, //go:registerparams, //go:linkname,
// //go:uintptrescapes and the rest on a func — as Stmt.Comments on the
// positioned declaration, where the emitter (goDirectives) prints them
// verbatim above it so gc applies them as it did to the source: an embed
// consumes the same relative asset paths as the unchanged package, a
// noinline keeps the call the asmcheck row measures. Only the directive
// lines between the preceding declaration and this declaration travel;
// unlike go/ast's Doc field, gc's pragma association does not require the
// comment to be adjacent. //go:build and //go:generate address the file and
// the tooling, not a declaration. The name is historical: the //go:embed path
// was the first to exist.
func (c *converter) attachEmbedDirectives(file *syntax.File) {
	byNamePos := map[uint][]syntax.Comment{}
	attach := func(name *ast.Ident, groups []*ast.CommentGroup) {
		for _, group := range groups {
			for _, comment := range group.List {
				if !isDeclDirective(comment.Text) {
					continue
				}
				pos := c.pos(name.Pos()).Offset()
				byNamePos[pos] = append(byNamePos[pos], syntax.Comment{Hash: c.pos(comment.Slash), Text: strings.TrimPrefix(comment.Text, "//")})
			}
		}
	}
	for _, f := range c.files {
		previousEnd := f.Package
		for _, decl := range f.Decls {
			var groups []*ast.CommentGroup
			for _, group := range f.Comments {
				if group.Pos() > previousEnd && group.End() < decl.Pos() {
					groups = append(groups, group)
				}
			}
			switch d := decl.(type) {
			case *ast.FuncDecl:
				attach(d.Name, groups)
			case *ast.GenDecl:
				if d.Tok != token.VAR {
					previousEnd = decl.End()
					continue
				}
				declGroups := groups
				if len(d.Specs) != 1 && d.Doc != nil {
					declGroups = nil
					for _, group := range groups {
						if group != d.Doc {
							declGroups = append(declGroups, group)
						}
					}
				}
				for _, spec := range d.Specs {
					v := spec.(*ast.ValueSpec)
					if len(v.Names) != 1 {
						continue
					}
					specGroups := declGroups
					if v.Doc != nil && v.Doc != d.Doc {
						specGroups = append(append([]*ast.CommentGroup(nil), declGroups...), v.Doc)
					}
					attach(v.Names[0], specGroups)
				}
			}
			previousEnd = decl.End()
		}
	}
	for _, stmt := range file.Stmts {
		var name *syntax.Lit
		switch d := stmt.Cmd.(type) {
		case *syntax.BashPPDecl:
			name = d.Name
		case *syntax.BashPPFuncDecl:
			name = d.Name
		}
		if name != nil {
			stmt.Comments = append(stmt.Comments, byNamePos[name.Pos().Offset()]...)
		}
	}
}

// isDeclDirective reports whether a comment line is a compiler directive
// that addresses the declaration it documents.
func isDeclDirective(text string) bool {
	if !strings.HasPrefix(text, "//go:") {
		return false
	}
	directive, _, _ := strings.Cut(strings.TrimPrefix(text, "//go:"), " ")
	directive, _, _ = strings.Cut(directive, "\t")
	return directive != "build" && directive != "generate"
}
