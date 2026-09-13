package interp

import (
	"strconv"

	"mvdan.cc/sh/v3/syntax"
)

// Sprint 165, lane frames-1: the qualified name a reported frame carries.
//
// Go names a function literal after the declaration it is written in:
// `main.f.func1`, `main.f.func2`, … number the literals directly inside f
// in source order, whether or not any of them has run; a literal inside a
// literal is `main.f.func1.1`, numbered among its own enclosing literal's
// direct literals. A generic declaration's frame is `main.gen[...]`, a
// method of a generic type `main.Box[...].M`. The name is a property of the
// source, so the frame table indexes the program's literals once, on first
// use, and answers every frame from that index.

// goSourceLiteralNames indexes every function literal of the program by its
// qualified name, built once per runner.
func (r *Runner) goSourceLiteralNames() map[*syntax.BashPPFuncLit]string {
	if r.goSourceLiteralNameIndex != nil {
		return r.goSourceLiteralNameIndex
	}
	names := map[*syntax.BashPPFuncLit]string{}
	r.goSourceLiteralNameIndex = names
	if r.bashPPGoSourceFile == nil {
		return names
	}
	// Package-level literals (an initialiser's `func() {…}`) are made by
	// the package's init function and named after it, `main.init.funcN`.
	globals := 0
	for _, stmt := range r.bashPPGoSourceFile.Stmts {
		if decl, ok := stmt.Cmd.(*syntax.BashPPFuncDecl); ok {
			goSourceIndexLiterals(decl.Body, goSourceDeclFrameName(decl), ".func", names)
			continue
		}
		syntax.Walk(stmt, func(node syntax.Node) bool {
			lit, ok := node.(*syntax.BashPPFuncLit)
			if !ok {
				return true
			}
			globals++
			name := "main.init.func" + strconv.Itoa(globals)
			names[lit] = name
			goSourceIndexLiterals(lit.Body, name, ".", names)
			return false
		})
	}
	return names
}

// goSourceIndexLiterals names the literals directly inside body, in source
// order, as parent+suffix+N, then each literal's own literals below it.
func goSourceIndexLiterals(body *syntax.Block, parent, suffix string, names map[*syntax.BashPPFuncLit]string) {
	if body == nil {
		return
	}
	n := 0
	syntax.Walk(body, func(node syntax.Node) bool {
		lit, ok := node.(*syntax.BashPPFuncLit)
		if !ok {
			return true
		}
		n++
		name := parent + suffix + strconv.Itoa(n)
		names[lit] = name
		goSourceIndexLiterals(lit.Body, name, ".", names)
		return false
	})
}

// goSourceDeclFrameName is the qualified name of a declared function's
// frame: `main.f`, `main.T.M`, `main.(*T).M`, and `[...]` for the type
// arguments of a generic declaration or receiver.
func goSourceDeclFrameName(decl *syntax.BashPPFuncDecl) string {
	if recv := decl.Receiver; recv != nil && recv.RecvType != nil {
		owner := recv.RecvType.Value
		if len(recv.TypeParams) > 0 {
			owner += "[...]"
		}
		if recv.Pointer {
			owner = "(*" + owner + ")"
		}
		return "main." + owner + "." + decl.Name.Value
	}
	name := "main." + decl.Name.Value
	if len(decl.TypeParams) > 0 {
		name += "[...]"
	}
	return name
}
