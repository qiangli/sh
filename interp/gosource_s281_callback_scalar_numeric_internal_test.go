//go:build full

package interp

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8

import (
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestS281CallbackScalarNumericComparison(t *testing.T) {
	r := &Runner{bashPPGoSource: true}
	floatMeta := &bashPPCollectionMeta{
		kind: "scalar",
		typ:  &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "float64"}},
	}
	for _, test := range []struct {
		name                string
		left, right         any
		leftMeta, rightMeta *bashPPCollectionMeta
	}{
		{name: "field left", left: float64(1), right: int(1), leftMeta: floatMeta},
		{name: "field right", left: int(1), right: float64(1), rightMeta: floatMeta},
		{name: "unequal", left: float64(1), right: int(2), leftMeta: floatMeta},
	} {
		t.Run(test.name, func(t *testing.T) {
			equal, err := r.bashPPCompareValues(test.left, test.leftMeta, false, test.right, test.rightMeta, false)
			if err != nil {
				t.Fatal(err)
			}
			if equal != (test.name != "unequal") {
				t.Fatalf("equal = %v", equal)
			}
		})
	}

	intMeta := &bashPPCollectionMeta{
		kind: "scalar",
		typ:  &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "int"}},
	}
	if _, err := r.bashPPCompareValues(float64(1), floatMeta, false, int(1), intMeta, false); err == nil {
		t.Fatal("declared float64 and int values compared without a type error")
	}
}
