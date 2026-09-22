// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"

	"mvdan.cc/sh/v3/syntax"
)

// bashPPPreparePackageConstants evaluates Go package constants in dependency
// order. Constant expressions have no runtime side effects, so unlike var
// initializers they may safely move ahead of intervening shell statements.
// The returned set tells the ordinary source-order loop which declarations
// have already been installed.
func (r *Runner) bashPPPreparePackageConstants(ctx context.Context, file *syntax.File) (map[syntax.Command]bool, error) {
	if file == nil || !file.GoSource {
		return nil, nil
	}
	type constantDecl struct {
		cmd   syntax.Command
		pos   syntax.Pos
		names []string
		exprs []syntax.BashPPExpr
	}
	var decls []*constantDecl
	byName := make(map[string]*constantDecl)
	for _, stmt := range file.Stmts {
		var decl *constantDecl
		switch cmd := stmt.Cmd.(type) {
		case *syntax.BashPPDecl:
			if cmd.Site == syntax.StartConst {
				decl = &constantDecl{cmd: cmd, pos: cmd.Pos(), names: []string{cmd.Name.Value}, exprs: []syntax.BashPPExpr{cmd.InitExpr}}
			}
		case *syntax.BashPPConstGroup:
			decl = &constantDecl{cmd: cmd, pos: cmd.Pos()}
			var previous syntax.BashPPExpr
			for _, spec := range cmd.Specs {
				if spec.InitExpr != nil {
					previous = spec.InitExpr
				}
				decl.names = append(decl.names, spec.Name.Value)
				decl.exprs = append(decl.exprs, previous)
			}
		}
		if decl == nil {
			continue
		}
		decls = append(decls, decl)
		for _, name := range decl.names {
			if name != "_" {
				byName[name] = decl
			}
		}
	}
	state := make(map[*constantDecl]uint8)
	prepared := make(map[syntax.Command]bool, len(decls))
	var visit func(*constantDecl) error
	visit = func(decl *constantDecl) error {
		switch state[decl] {
		case 1:
			return fmt.Errorf("%sBASHPP-ECONST-CYCLE: constant initialization cycle", r.bashErrPrefix(decl.pos))
		case 2:
			return nil
		}
		state[decl] = 1
		for _, expr := range decl.exprs {
			if expr == nil {
				continue
			}
			var dependencyErr error
			syntax.Walk(expr, func(node syntax.Node) bool {
				ident, ok := node.(*syntax.BashPPIdent)
				if !ok {
					return true
				}
				dependency := byName[ident.Name.Value]
				if dependency == nil || dependency == decl {
					return true
				}
				if err := visit(dependency); err != nil {
					dependencyErr = err
					return false
				}
				return true
			})
			if dependencyErr != nil {
				return dependencyErr
			}
		}
		switch cmd := decl.cmd.(type) {
		case *syntax.BashPPDecl:
			r.bashPPDeclare(ctx, r.bashPPBindDecl(cmd))
		case *syntax.BashPPConstGroup:
			r.bashPPConstGroup(ctx, r.bashPPBindConstGroup(cmd))
		}
		if r.exit.code != 0 {
			return ExitStatus(r.exit.code)
		}
		state[decl] = 2
		prepared[decl.cmd] = true
		return nil
	}
	for _, decl := range decls {
		if err := visit(decl); err != nil {
			return nil, err
		}
	}
	return prepared, nil
}
