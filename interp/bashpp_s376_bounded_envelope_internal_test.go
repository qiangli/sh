package interp

import (
	"testing"
)

// Sprint: #376; Story: #1507; Story-ID: 0cde6d446a94
//
// Boundary regression for the return-boundary fast wrapping: only
// demonstrably bounded scalar envelopes may skip shell-object validation.
// Anything else must fall back to the validated cell, preserving the
// original validation and interface/type semantics.
func TestGoSourceBoundedScalarCompositeBoundary(t *testing.T) {
	scalar := func(text string) bashPPBridgeValue {
		return bashPPBridgeValue{Kind: "uint", Type: "uint8", Text: text}
	}
	bounded := bashPPBridgeValue{Kind: "struct", Type: "image/color.RGBA", Elements: []bashPPBridgeValue{
		scalar("2"), scalar("2"), scalar("255"), scalar("255"),
	}}
	if !goSourceBoundedScalarComposite(bounded) {
		t.Fatal("bounded 4-scalar positional envelope rejected")
	}
	named := bashPPBridgeValue{Kind: "struct", Type: "image/color.RGBA", Fields: map[string]bashPPBridgeValue{
		"R": scalar("1"), "G": scalar("2"), "B": scalar("3"), "A": scalar("255"),
	}}
	if !goSourceBoundedScalarComposite(named) {
		t.Fatal("bounded named-field envelope rejected")
	}
	withIface := bounded
	withIface.Interface = "image/color.Color"
	if !goSourceBoundedScalarComposite(withIface) {
		t.Fatal("bounded envelope with interface metadata rejected")
	}
	many := bashPPBridgeValue{Kind: "struct", Type: "image/color.RGBA"}
	for i := 0; i < goSourceBoundedScalarEnvelopeLeafLimit+1; i++ {
		many.Elements = append(many.Elements, scalar("1"))
	}
	for _, tc := range []struct {
		name  string
		value bashPPBridgeValue
	}{
		{"empty", bashPPBridgeValue{Kind: "struct", Type: "image/color.RGBA"}},
		{"over leaf limit", many},
		{"non struct", bashPPBridgeValue{Kind: "string", Type: "string", Text: "x"}},
		{"missing type", bashPPBridgeValue{Kind: "struct", Elements: []bashPPBridgeValue{scalar("1")}}},
		{"nested composite leaf", bashPPBridgeValue{Kind: "struct", Type: "T", Elements: []bashPPBridgeValue{bounded}}},
		{"handle leaf", bashPPBridgeValue{Kind: "struct", Type: "T", Elements: []bashPPBridgeValue{{Kind: "uint", Type: "uint8", Text: "1", Handle: 7}}}},
		{"handle carrier", func() bashPPBridgeValue { v := bounded; v.Handle = 7; return v }()},
		{"origin carrier", func() bashPPBridgeValue { v := bounded; v.Origin = 7; return v }()},
		{"callback carrier", func() bashPPBridgeValue { v := bounded; v.Callbacks = true; return v }()},
		{"already deferred", func() bashPPBridgeValue { v := bounded; v.deferredNativeComposite = true; return v }()},
		{"non scalar leaf", bashPPBridgeValue{Kind: "struct", Type: "T", Elements: []bashPPBridgeValue{{Kind: "slice", Type: "[]int"}}}},
	} {
		if goSourceBoundedScalarComposite(tc.value) {
			t.Fatalf("%s: unbounded composite accepted as bounded envelope", tc.name)
		}
	}
}

// An unbounded composite through the trusted wrapper must observe exactly
// the validated cell: same string form and same interface attribution.
func TestGoSourceNativeValueCellTrustedFallsBack(t *testing.T) {
	scalar := func(text string) bashPPBridgeValue {
		return bashPPBridgeValue{Kind: "uint", Type: "uint8", Text: text}
	}
	unbounded := bashPPBridgeValue{
		Kind:      "struct",
		Type:      "main.Big",
		Interface: "image/color.Color",
		Fields: map[string]bashPPBridgeValue{
			"nested": {Kind: "struct", Type: "main.Inner", Fields: map[string]bashPPBridgeValue{"N": scalar("1")}},
		},
	}
	if goSourceBoundedScalarComposite(unbounded) {
		t.Fatal("nested composite accepted as bounded envelope")
	}
	got := goSourceNativeValueCellTrusted(unbounded)
	want := goSourceNativeValueCell(unbounded)
	if got.vr.String() != want.vr.String() {
		t.Fatalf("trusted fallback string %q, validated %q", got.vr.String(), want.vr.String())
	}
	if (got.interfaceValue == nil) != (want.interfaceValue == nil) {
		t.Fatal("trusted fallback dropped interface attribution")
	}
}

// The bounded fast path must render exactly like the validated cell.
func TestGoSourceNativeValueCellTrustedBoundedAgrees(t *testing.T) {
	bounded := bashPPBridgeValue{Kind: "struct", Type: "image/color.RGBA", Elements: []bashPPBridgeValue{
		{Kind: "uint", Type: "uint8", Text: "2"},
		{Kind: "uint", Type: "uint8", Text: "2"},
		{Kind: "uint", Type: "uint8", Text: "255"},
		{Kind: "uint", Type: "uint8", Text: "255"},
	}}
	got := goSourceNativeValueCellTrusted(bounded)
	want := goSourceNativeValueCell(bounded)
	if got.vr.String() != want.vr.String() {
		t.Fatalf("bounded fast string %q, validated %q", got.vr.String(), want.vr.String())
	}
	if got.typeName != want.typeName {
		t.Fatalf("bounded fast type %q, validated %q", got.typeName, want.typeName)
	}
}
