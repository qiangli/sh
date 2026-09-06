// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func TestBashPPCollectionRuntimeIdentity(t *testing.T) {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(
		"a := [1]int{1}\ns := []int{1}\nm := map[string]int{\"k\": 1}\n"), "identity.bpp")
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Lang(syntax.LangBashPP))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"a": "array", "s": "slice", "m": "map"} {
		cell := r.bashPPScope.lookup(name)
		if cell == nil || cell.object == nil || cell.object.collection == nil || cell.object.collection.kind != want {
			t.Fatalf("%s identity = %#v, want %s", name, cell, want)
		}
	}
}

func TestBashPPTaskSnapshotClonesCollectionMetadata(t *testing.T) {
	childMeta := &bashPPCollectionMeta{kind: "slice"}
	parentMeta := &bashPPCollectionMeta{kind: "array", sequence: []*bashPPCollectionMeta{childMeta}}
	parentValue := []any{[]any{1}}
	parent := &Runner{bashPPScope: newBashPPScope(nil)}
	identity := &bashPPObjectIdentity{owner: "a", collection: parentMeta}
	parent.bashPPScope.entries["a"] = &bashPPCell{vr: expand.NewObject(parentValue), object: identity}

	child := &Runner{bashPPScope: newBashPPCloner().clone(parent.bashPPScope)}
	if err := cloneBashPPTaskCells(child, newBashPPObjectCloner()); err != nil {
		t.Fatal(err)
	}
	childCell := child.bashPPScope.lookup("a")
	childCell.vr.Obj.([]any)[0].([]any)[0] = 2
	childCell.object.collection.sequence[0] = nil
	if got := parentValue[0].([]any)[0]; got != 1 {
		t.Fatalf("task payload mutation escaped snapshot: %v", got)
	}
	if parentMeta.sequence[0] != childMeta {
		t.Fatal("task metadata mutation escaped snapshot")
	}
}
