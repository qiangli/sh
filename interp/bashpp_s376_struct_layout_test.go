package interp

import (
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// Exercise comparison without a Go-source runner: Bash# must enumerate scalar
// fields even when they carry no runtime layout, including inside arrays.
func TestS376SparseStructComparisonAndCopy(t *testing.T) {
	typ := &syntax.BashPPStructType{Fields: []*syntax.BashPPField{{
		Names:         []*syntax.Lit{{Value: "A"}, {Value: "B"}},
		FieldTypeExpr: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "int"}},
	}}}
	r := &Runner{}
	value, meta := r.bashPPZeroValue(typ)
	copyValue, copyMeta := bashPPCopyArrayValue(value, meta)
	check := func(want bool) {
		t.Helper()
		got, err := bashPPCompareValues(value, meta, false, copyValue, copyMeta, false)
		if err != nil || got != want {
			t.Fatalf("struct comparison = %v, %v; want %v", got, err, want)
		}
	}
	check(true)
	bashPPStorageSetField(copyValue.(map[string]any), copyMeta.mapping, "A", "9", nil)
	check(false)
	bashPPStorageSetField(value.(map[string]any), meta.mapping, "A", "9", nil)
	check(true)
	arrayType := &syntax.BashPPCollectionType{Kind: "array", Length: &syntax.Lit{Value: "1"}, Element: typ}
	arrayMeta := &bashPPCollectionMeta{kind: "array", typ: arrayType, sequence: []*bashPPCollectionMeta{meta}}
	array := []any{value}
	other, otherMeta := bashPPCopyArrayValue(array, arrayMeta)
	nested := other.([]any)[0].(map[string]any)
	bashPPStorageSetField(nested, otherMeta.sequence[0].mapping, "B", "4", nil)
	if equal, err := bashPPCompareValues(array, arrayMeta, false, other, otherMeta, false); err != nil || equal {
		t.Fatalf("nested scalar mismatch lost: equal=%v err=%v", equal, err)
	}
}

func TestS376StructInitializerClearsLayout(t *testing.T) {
	value := make(map[string]any)
	meta := &bashPPCollectionMeta{kind: "struct"}
	child := &bashPPCollectionMeta{kind: "func"}
	bashPPSetInitialStructField(value, meta, "F", nil, child)
	bashPPSetInitialStructField(value, meta, "F", "closure", nil)
	if got := bashPPLayoutGet(meta.mapping, "F"); got != nil {
		t.Fatal("initializer retained zero-value metadata for the replacement")
	}
	if got, ok := bashPPStorageGet(value, "F"); !ok || got != "closure" {
		t.Fatal("field disappeared with its layout")
	}
}

func BenchmarkS376ScalarStructStorage(b *testing.B) {
	typ := &syntax.BashPPStructType{Fields: []*syntax.BashPPField{{
		Names:         []*syntax.Lit{{Value: "A"}, {Value: "B"}, {Value: "C"}, {Value: "D"}},
		FieldTypeExpr: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "int"}},
	}}}
	r := &Runner{}
	b.ReportAllocs()
	for b.Loop() {
		value, meta := r.bashPPZeroValue(typ)
		bashPPCopyArrayValue(value, meta)
	}
}

// Zero creation plus value-copy should not allocate two scalar-only layout
// maps. This small allocation budget regresses before sparse layout storage.
func TestS376ScalarStructAllocationBudget(t *testing.T) {
	typ := &syntax.BashPPStructType{Fields: []*syntax.BashPPField{{
		Names:         []*syntax.Lit{{Value: "A"}, {Value: "B"}, {Value: "C"}, {Value: "D"}},
		FieldTypeExpr: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "int"}},
	}}}
	r := &Runner{}
	allocs := testing.AllocsPerRun(100, func() {
		value, meta := r.bashPPZeroValue(typ)
		bashPPCopyArrayValue(value, meta)
	})
	if allocs > 12 {
		t.Fatalf("scalar struct creation and copy: %.0f allocations, budget 12", allocs)
	}
}
