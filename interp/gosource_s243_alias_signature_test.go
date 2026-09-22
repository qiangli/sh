//go:build full

package interp

import (
	"mvdan.cc/sh/v3/syntax"
	"testing"
)

func TestS243AliasSignatureImportIdentity(t *testing.T) {
	named := func(name string) *syntax.BashPPNamedType {
		return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: name}}
	}
	fields := func(name string) []*syntax.BashPPField { return []*syntax.BashPPField{{FieldTypeExpr: named(name)}} }
	r := &Runner{bashPPImports: map[string]string{"a": "example.com/one", "b": "example.com/one", "other": "example.com/two"}, bashPPTypes: map[string]bashPPType{
		"Alias":   {alias: true, typeExpr: named("a.T")},
		"Defined": {typeExpr: named("a.T")},
	}}
	for _, tc := range []struct {
		left, right string
		want        bool
	}{
		{"a.T", "b.T", true},
		{"Alias", "b.T", true},
		{"a.T", "other.T", false},
		{"a.T", "b.U", false},
		{"Defined", "a.T", false},
	} {
		if got := r.bashPPAliasedSignatureEqual(nil, fields(tc.left), nil, fields(tc.right)); got != tc.want {
			t.Errorf("%s vs %s: %v, want %v", tc.left, tc.right, got, tc.want)
		}
	}
	if r.bashPPAliasedSignatureEqual(fields("a.T"), nil, nil, fields("b.T")) {
		t.Fatal("parameter and result roles merged")
	}
}
