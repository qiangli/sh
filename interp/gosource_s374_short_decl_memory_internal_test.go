// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// A short declaration transaction only needs old values for names on its
// left-hand side. Its rollback storage must not grow with unrelated locals:
// functions such as the register-pressure regression in Go issue 80188 have
// many live locals and execute short declarations in an outer loop.
func TestGoSourceShortDeclSnapshotBoundedByTargets(t *testing.T) {
	r := &Runner{bashPPScope: newBashPPScope(nil), bashPPGoSource: true}
	for i := range 64 {
		name := fmt.Sprintf("unrelated%d", i)
		r.bashPPScope.entries[name] = &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String}}
	}
	r.bashPPScope.entries["reused"] = &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: "old"}}

	txn, ok := r.bashPPBeginShortDecl(&syntax.BashPPShortDecl{
		Lhs: []*syntax.Lit{{Value: "reused"}, {Value: "fresh"}},
	})
	if !ok {
		t.Fatal("short declaration transaction was rejected")
	}
	if got, want := len(txn.saved), 1; got != want {
		t.Fatalf("transaction retained %d cells, want %d left-hand target; unrelated locals must not be snapshotted", got, want)
	}
}

// Scalar fields need no side metadata. Keeping a second hash map containing
// one nil entry per field roughly doubles every small struct allocation.
func TestGoSourceScalarStructHasNoEmptyLayoutMap(t *testing.T) {
	integer := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "int"}}
	typ := &syntax.BashPPStructType{Fields: []*syntax.BashPPField{
		{Names: []*syntax.Lit{{Value: "A"}, {Value: "B"}, {Value: "C"}, {Value: "D"}}, FieldTypeExpr: integer},
	}}
	_, meta := new(Runner).bashPPZeroValue(typ)
	if meta == nil || meta.kind != "struct" {
		t.Fatalf("metadata = %#v, want struct metadata", meta)
	}
	if meta.mapping != nil {
		t.Fatalf("scalar struct retained an empty layout map with %d entries", len(meta.mapping))
	}
}
