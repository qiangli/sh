// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

// Sprint: #243; Story: #674; Story-ID: 63073886bfce
//
// Identity of a reused local type name at the native bridge.
//
// A name declared in two scopes — `type s struct{ f int }` in one method
// and `type s struct{ g bool }` in another, or a package-level `Int`
// shadowed by a function-local `Int` — denotes distinct Go types. The
// helper's type registry is keyed by spelling, so the descriptor builder
// used to drop every declaration of such a name and every value of one
// was refused as an unregistered bridge type. The evaluator already
// resolves a named-type reference to its declaration lexically
// (bashpp_sprint162_type_scope.go); this file gives each function-local
// declaration of a reused name its own helper identity and spells a
// reference by that identity when it crosses. A package-level
// declaration keeps the plain name, which is what every reference outside
// the shadowing scopes means. A reference without a source position
// cannot be placed and still spells the plain name, so a program whose
// declarations are all function-local stays refused rather than guessed.

import (
	"crypto/sha256"
	"fmt"
	"strconv"

	"mvdan.cc/sh/v3/syntax"
)

// bashPPScopedLocalDecl is one function-local declaration of a reused
// type name: the declaration node and the scope key
// goSourceLocalTypeScope answers for a reference to it.
type bashPPScopedLocalDecl struct {
	decl  *syntax.BashPPDecl
	scope string
}

// bashPPScopedLocalKey is the table key of a declaration: the name and
// the scope key of the declaration a reference resolves to.
func bashPPScopedLocalKey(name, scope string) string {
	return name + "@" + scope
}

// bashPPScopedLocalName is the helper identity of one scoped declaration.
// It is a plain identifier, so a composed spelling — `[]s`, `*s`,
// `map[string]s` — parses in the helper's resolver and reaches the
// registered name through it.
func bashPPScopedLocalName(key string) string {
	return fmt.Sprintf("bppScoped_%x", sha256.Sum256([]byte(key)))
}

// bashPPScopedLocalDecls collects, per reused name, the function-local
// declarations that can be registered under their own identity, and
// reports which reused names keep a single package-level declaration
// under the plain name. A generic or blank declaration is not scoped.
func bashPPScopedLocalDecls(file *syntax.File, ambiguous map[string]bool) (scoped map[string]bashPPScopedLocalDecl, packageLevel map[string]*syntax.BashPPDecl) {
	scoped = map[string]bashPPScopedLocalDecl{}
	packageLevel = map[string]*syntax.BashPPDecl{}
	if file == nil || len(ambiguous) == 0 {
		return scoped, packageLevel
	}
	top := map[*syntax.BashPPDecl]bool{}
	for _, stmt := range file.Stmts {
		if d, ok := stmt.Cmd.(*syntax.BashPPDecl); ok {
			top[d] = true
		}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		d, ok := node.(*syntax.BashPPDecl)
		if !ok || d.Site != syntax.StartTypeDecl || d.Name == nil || d.DeclTypeExpr == nil || len(d.TypeParams) > 0 {
			return true
		}
		name := d.Name.Value
		if !ambiguous[name] || name == "_" {
			return true
		}
		if top[d] {
			packageLevel[name] = d
			return true
		}
		pos := d.Pos()
		if !pos.IsValid() {
			return true
		}
		scope := strconv.FormatUint(uint64(pos.Offset()), 10)
		scoped[bashPPScopedLocalKey(name, scope)] = bashPPScopedLocalDecl{decl: d, scope: scope}
		return true
	})
	return scoped, packageLevel
}

// bashPPScopedLocalTypeName is the bashPPBridgeTypeScope of this runner:
// a reference to a function-local declaration of a reused name spells
// the helper identity registered for that declaration.
func (r *Runner) bashPPScopedLocalTypeName(named *syntax.BashPPNamedType) (string, bool) {
	if !r.bashPPGoSource || named == nil || named.Name == nil || len(named.TypeArgs) > 0 {
		return "", false
	}
	r.bashPPLocalTypeDescriptors()
	cache := r.bashPPTools.localTypes
	if cache == nil || len(cache.scoped) == 0 {
		return "", false
	}
	scope, known := r.goSourceLocalTypeScope(named)
	if !known || scope == "" {
		return "", false
	}
	name, ok := cache.scoped[bashPPScopedLocalKey(named.Name.Value, scope)]
	return name, ok
}
