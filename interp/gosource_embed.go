// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// bashPPEmbedDecl is the declaration-only part of a Go embed directive. The
// native dependency helper compiles the directive, while original function
// bodies remain interpreted.
type bashPPEmbedDecl struct {
	Name       string
	Type       string
	Directives []string
}

const bashPPEmbedSymbolPrefix = "\x00gosource.embed."

func bashPPGoSourceEmbedDecls(file *syntax.File) []bashPPEmbedDecl {
	if file == nil || !file.GoSource {
		return nil
	}
	var decls []bashPPEmbedDecl
	for _, stmt := range file.Stmts {
		decl, ok := stmt.Cmd.(*syntax.BashPPDecl)
		if !ok || decl.Site != syntax.StartVar || decl.Name == nil || decl.DeclTypeExpr == nil {
			continue
		}
		var directives []string
		for _, comment := range stmt.Comments {
			if strings.HasPrefix(comment.Text, "go:embed ") || strings.HasPrefix(comment.Text, "go:embed\t") {
				directives = append(directives, comment.Text)
			}
		}
		if len(directives) > 0 {
			decls = append(decls, bashPPEmbedDecl{
				Name: decl.Name.Value, Type: bashPPBridgeTypeText(decl.DeclTypeExpr), Directives: directives,
			})
		}
	}
	return decls
}

func (r *Runner) bashPPGoSourceEmbedRequest() ([]bashPPEmbedDecl, string) {
	decls := bashPPGoSourceEmbedDecls(r.bashPPGoSourceFile)
	if len(decls) == 0 {
		return nil, ""
	}
	name := r.bashPPGoSourceFile.Name
	if name == "" {
		return decls, r.Dir
	}
	if !filepath.IsAbs(name) {
		base := r.Dir
		if r.bashPPTools.moduleDir != "" {
			base = r.bashPPTools.moduleDir
		}
		name = filepath.Join(base, name)
	}
	return decls, filepath.Dir(name)
}

func (r *Runner) bashPPNativeEmbedDeclaration(d *syntax.BashPPDecl) bool {
	if !r.bashPPGoSource || d == nil || d.Site != syntax.StartVar || d.Name == nil {
		return false
	}
	found := false
	for _, embed := range bashPPGoSourceEmbedDecls(r.bashPPGoSourceFile) {
		if embed.Name == d.Name.Value {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	req, err := r.bashPPEvalRequest()
	if err == nil {
		var values []bashPPBridgeValue
		values, err = r.bashPPNativeRequest(r.ectx, req, bashPPBridgeRequest{Op: "get", Selector: bashPPEmbedSymbolPrefix + d.Name.Value})
		if err == nil {
			if len(values) != 1 {
				err = fmt.Errorf("gosource: embedded declaration returned %d values", len(values))
			} else if d.Name.Value != "_" {
				r.bashPPBindNativeValue(d.Name.Value, values[0])
			}
		}
	}
	if err != nil {
		r.exit.fatal(&goSourceError{prefix: r.bashErrPrefix(d.Pos()), err: err})
	}
	return true
}
