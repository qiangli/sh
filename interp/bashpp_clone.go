package interp

import "mvdan.cc/sh/v3/syntax"

// bashPPCallbackSharedCells extends a callback's exact lexical capture with
// the package variables its original Go body is entitled to use. Package
// variables are shared storage in Go, not closure captures: copying them would
// lose mutations, while the GoSource task clone otherwise omits them entirely.
//
// The callback position authenticates the source package before its flattened
// package tag is consulted. The cells come from the callback's registration-
// time scope, not the Runner's later live root, and only the root scope is
// considered. Thus function-frame locals which were not lexically captured
// and globals belonging to another flattened package remain absent.
func (r *Runner) bashPPCallbackSharedCells(fn *bashPPFunc, capture map[*bashPPCell]bool) map[*bashPPCell]bool {
	pos := bashPPCallbackSourcePos(fn)
	if r == nil || fn == nil || fn.scope == nil || r.bashPPGoSourceFile == nil || !pos.IsValid() {
		return capture
	}
	if _, ok := r.bashPPGoSourceFile.SourceAt(pos); !ok {
		return capture
	}
	packageTag := r.goSourcePackageAt(pos)
	root := fn.scope
	for root.parent != nil {
		root = root.parent
	}
	shared := make(map[*bashPPCell]bool, len(capture)+len(root.entries))
	for cell := range capture {
		shared[cell] = true
	}
	for name, cell := range root.entries {
		if cell != nil && !cell.constant && goSourceLinkedPackage(name) == packageTag {
			shared[cell] = true
		}
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
