package interp

// Sprint: #243; Story: #674; Story-ID: 63073886bfce

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// goSourceLinknameDeclaration binds a package-level variable carrying a
// two-argument //go:linkname directive to the target package variable's cell.
// gosource has already asked gc/go/types to validate the pragma and unsafe
// import; this is only the runtime storage-link step after packages have been
// flattened for interpreted execution.
func (r *Runner) goSourceLinknameDeclaration(d *syntax.BashPPDecl) (error, bool) {
	if !r.bashPPGoSource || d.Site != syntax.StartVar || r.bashPPGoSourceFile == nil || !r.goSourceTopLevelDecl(d) {
		return nil, false
	}
	local, target, ok := r.goSourceDeclLinkname(d)
	if !ok {
		return nil, false
	}
	if local != goSourceDeclaredName(d.Name.Value) {
		return fmt.Errorf("go:linkname local name %q does not name declaration %q", local, goSourceDeclaredName(d.Name.Value)), true
	}
	root := r.bashPPScope
	for root.parent != nil {
		root = root.parent
	}
	cell := root.linknames[target]
	if cell == nil {
		return fmt.Errorf("go:linkname target %q is not a linked package variable", target), true
	}
	if _, exists := r.bashPPScope.entries[d.Name.Value]; exists && d.Name.Value != "_" {
		return fmt.Errorf("%s redeclared in this block", d.Name.Value), true
	}
	r.bashPPScope.entries[d.Name.Value] = cell
	return nil, true
}

// goSourceRegisterLinknameTarget publishes a successfully evaluated mapped
// package variable under its linker spelling. It runs after the declaration's
// initializer, so an alias never creates or initializes a second cell.
func (r *Runner) goSourceRegisterLinknameTarget(d *syntax.BashPPDecl) {
	if !r.bashPPGoSource || d.Site != syntax.StartVar || r.bashPPGoSourceFile == nil || !r.goSourceTopLevelDecl(d) {
		return
	}
	if r.exit.code != 0 || r.exit.exiting || r.exit.fatalExit {
		return
	}
	source, ok := r.bashPPGoSourceFile.SourceAt(d.Pos())
	if !ok || source.PackagePath == "" {
		return
	}
	cell := r.bashPPScope.lookup(d.Name.Value)
	if cell == nil {
		return
	}
	root := r.bashPPScope
	for root.parent != nil {
		root = root.parent
	}
	if root.linknames == nil {
		root.linknames = make(map[string]*bashPPCell)
	}
	key := source.PackagePath + "." + goSourceDeclaredName(d.Name.Value)
	if prior := root.linknames[key]; prior == nil {
		root.linknames[key] = cell
	}
}

func (r *Runner) goSourceTopLevelDecl(d *syntax.BashPPDecl) bool {
	for _, stmt := range r.bashPPGoSourceFile.Stmts {
		if stmt.Cmd == d {
			return true
		}
	}
	return false
}

func (r *Runner) goSourceDeclLinkname(d *syntax.BashPPDecl) (local, target string, ok bool) {
	for _, stmt := range r.bashPPGoSourceFile.Stmts {
		if stmt.Cmd != d {
			continue
		}
		for _, comment := range stmt.Comments {
			fields := strings.Fields(comment.Text)
			if len(fields) == 3 && fields[0] == "go:linkname" {
				return fields[1], fields[2], true
			}
		}
		break
	}
	return "", "", false
}
