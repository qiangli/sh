//go:build full

// Sprint: #219; Story: #462; Story-ID: e348d2c13248
package interp

import (
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/syntax"
)

func TestS219BridgeTypeSpelling(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{"*any", "*any"},
		{"func() any", "func()(any)"},
		{"func(*any) (any, error)", "func(*any)(any,error)"},
		{"map[string][]func(*any) map[any]*interface{}", "map[string][]func(*any)(map[any]*interface{})"},
		{"Named[func() any, *interface{}]", "Named[func()(any),*interface{}]"},
	} {
		t.Run(test.input, func(t *testing.T) {
			typ := syntax.BashPPTypeExprFromText(test.input)
			qt.Assert(t, qt.IsNotNil(typ))
			qt.Assert(t, qt.Equals(bashPPBridgeTypeText(typ), test.want))
		})
	}
	variadic := &syntax.BashPPFuncType{Params: []*syntax.BashPPField{
		{FieldTypeExpr: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}},
		{FieldTypeExpr: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "any"}}, Ellipsis: syntax.NewPos(1, 1, 1)},
	}}
	qt.Assert(t, qt.Equals(bashPPBridgeTypeText(variadic), "func(string,...any)()"))

	// An absent type still follows the old invalid-spelling path, so helper
	// registration rejects it instead of silently authenticating a type guess.
	qt.Assert(t, qt.Equals(bashPPBridgeTypeText(nil), "<inferred>"))
}
