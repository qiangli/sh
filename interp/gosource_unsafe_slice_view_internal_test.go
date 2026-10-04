package interp

import (
	"go/token"
	"go/types"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestGoSourceUnsafeSliceHeaderViewUsesImportedStructShape(t *testing.T) {
	pkg := types.NewPackage("internal/unsafeheader", "unsafeheader")
	header := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "SliceHeader", nil), types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, pkg, "Data", types.Typ[types.UnsafePointer], false),
		types.NewField(token.NoPos, pkg, "Len", types.Typ[types.Int], false),
		types.NewField(token.NoPos, pkg, "Cap", types.Typ[types.Int], false),
	}, nil), nil)
	r := &Runner{
		bashPPGoSource: true,
		bashPPImports:  map[string]string{"unsafeheader": "internal/unsafeheader", "unsafe": "unsafe"},
		bashPPTools:    bashPPToolchain{nativeTypes: map[string]types.Type{}},
	}
	r.bashPPTools.nativeTypes["internal/unsafeheader.Slice"] = header
	source := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "unsafeheader.Slice"}}
	target := &syntax.BashPPCollectionType{
		Kind:    "slice",
		Element: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "int32"}},
	}
	if view := r.goSourceUnsafeSliceHeaderView(source, target); view == nil {
		t.Fatal("authenticated imported slice header was not recognized")
	}
	fields, ok := r.goSourceUnsafeSliceHeaderFields(source)
	if !ok || len(bashPPFlatFields(fields)) != 3 {
		t.Fatalf("imported slice header fields = %#v, %v; want authenticated local shape", fields, ok)
	}
}
