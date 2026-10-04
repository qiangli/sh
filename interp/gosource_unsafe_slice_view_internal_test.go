package interp

import (
	"go/token"
	"go/types"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestGoSourceUnsafeSliceHeaderViewUsesImportedStructShape(t *testing.T) {
	pkg := types.NewPackage("example/header", "header")
	header := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "SliceHeader", nil), types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, pkg, "Data", types.Typ[types.UnsafePointer], false),
		types.NewField(token.NoPos, pkg, "Len", types.Typ[types.Int], false),
		types.NewField(token.NoPos, pkg, "Cap", types.Typ[types.Int], false),
	}, nil), nil)
	r := &Runner{
		bashPPGoSource: true,
		bashPPImports:  map[string]string{"header": "example/header", "unsafe": "unsafe"},
		bashPPTools:    bashPPToolchain{nativeTypes: map[string]types.Type{}},
	}
	r.bashPPTools.nativeTypes["example/header.SliceHeader"] = header
	source := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "header.SliceHeader"}}
	target := &syntax.BashPPCollectionType{
		Kind:    "slice",
		Element: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "int32"}},
	}
	if view := r.goSourceUnsafeSliceHeaderView(source, target); view == nil {
		t.Fatal("authenticated imported slice header was not recognized")
	}
}
