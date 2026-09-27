package interp

import "mvdan.cc/sh/v3/syntax"

// bashPPCallbackSharedCells extends a callback's exact lexical capture with
// the package variables its original Go body is entitled to use. Package
// variables are shared storage in Go, not closure captures: copying them would
// lose mutations, while the GoSource task clone otherwise omits them entirely.
//
// The callback position authenticates the source package before its flattened
// package tag is consulted. The cells come from the callback's registration-
// time scope, not the Runner's later live root. Their names come from actual
// top-level variable declarations at positions owned by that package; a local
// which merely resembles a flattened name is never admitted. The outermost
// matching binding in the captured scope chain is the package cell: snapshots
// and testing frames can put that cell above an otherwise empty root, while a
// nearer binding can be an ordinary local shadow.
func (r *Runner) bashPPCallbackSharedCells(fn *bashPPFunc, capture map[*bashPPCell]bool) map[*bashPPCell]bool {
	pos := bashPPCallbackSourcePos(fn)
	if r == nil || fn == nil || fn.scope == nil || r.bashPPGoSourceFile == nil || !pos.IsValid() {
		return capture
	}
	file := r.bashPPGoSourceFile
	if _, ok := file.SourceAt(pos); !ok {
		return capture
	}
	packageTag := r.goSourcePackageAt(pos)
	packageNames := make(map[string]bool)
	for _, stmt := range file.Stmts {
		decl, ok := stmt.Cmd.(*syntax.BashPPDecl)
		if !ok || decl.Site != syntax.StartVar || decl.Name == nil {
			continue
		}
		if _, ok := file.SourceAt(decl.Pos()); !ok || r.goSourcePackageAt(decl.Pos()) != packageTag {
			continue
		}
		packageNames[decl.Name.Value] = true
	}
	packageCells := make(map[string]*bashPPCell, len(packageNames))
	for scope := fn.scope; scope != nil; scope = scope.parent {
		for name, cell := range scope.entries {
			if packageNames[name] && cell != nil && !cell.constant {
				// Walking outwards deliberately replaces a local shadow with the
				// package binding. Lexical captures already retain any local the
				// callback body actually names.
				packageCells[name] = cell
			}
		}
	}
	shared := make(map[*bashPPCell]bool, len(capture)+len(packageCells))
	for cell := range capture {
		shared[cell] = true
	}
	for _, cell := range packageCells {
		shared[cell] = true
	}
	return shared
}

func bashPPCallbackSourcePos(fn *bashPPFunc) syntax.Pos {
	if fn == nil {
		return syntax.Pos{}
	}
	if fn.decl != nil {
		return fn.decl.Pos()
	}
	if fn.lit != nil {
		return fn.lit.Pos()
	}
	return syntax.Pos{}
}
