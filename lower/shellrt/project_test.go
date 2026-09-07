// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package shellrt

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
)

// The expectations below are the interpreter's own recorded bytes, not a
// restatement of this file's implementation. Where a canonical lowering
// fixture records the output it is named. These are helper-level checks; they
// do not claim any profile passes end to end.

type projPair struct {
	First  string
	Second int
}

type projHolder struct {
	Item projPair
}

// tests/lowering/profile-additional/recursive-substitution.bpp records stdout
// {"Item":{"First":"n","Second":7}} for `echo "$h"` over this exact shape.
func TestProjectRichRootIsJSON(t *testing.T) {
	h := projHolder{Item: projPair{First: "n", Second: 7}}
	const want = `{"Item":{"First":"n","Second":7}}`
	if got := Project(h, KindObject); got != want {
		t.Fatalf("struct root: got %s want %s", got, want)
	}
	if got := Project(map[string]int{"b": 2, "a": 1}, KindObject); got != `{"a":1,"b":2}` {
		t.Fatalf("map root: got %s", got)
	}
	if got := Project([]int{1, 2}, KindObject); got != `[1,2]` {
		t.Fatalf("slice root: got %s", got)
	}
}

// An imported package call's result is bound as an object by the interpreter
// regardless of the result's Go type (interp/bashpp_readonly.go's
// bashPPShortDeclImported calls expand.NewObject unconditionally), so a string
// result root renders JSON-quoted rather than bare.
func TestProjectImportedStringRootIsQuoted(t *testing.T) {
	if got := Project("incremental", KindObject); got != `"incremental"` {
		t.Fatalf("imported string root: got %s want %q", got, `"incremental"`)
	}
	// The same bytes as a native scalar root are plain. Only the kind differs;
	// the value is identical, so no content heuristic could tell them apart.
	if got := Project("incremental", KindScalar); got != "incremental" {
		t.Fatalf("scalar string root: got %s", got)
	}
}

func TestProjectNilRichRootIsNull(t *testing.T) {
	if got := Project(nil, KindObject); got != "null" {
		t.Fatalf("nil rich root: got %s want null", got)
	}
	var nilMap map[string]int
	if got := Project(nilMap, KindObject); got != "null" {
		t.Fatalf("nil map root: got %s want null", got)
	}
}

// A scalar reached by selection or indexing keeps plain text even though its
// enclosing root is JSON.
func TestProjectSelectedScalarIsPlain(t *testing.T) {
	h := projHolder{Item: projPair{First: "n", Second: 7}}
	if got := Project(h.Item.Second, KindScalar); got != "7" {
		t.Fatalf("selected int: got %s want 7", got)
	}
	if got := Project(h.Item.First, KindScalar); got != "n" {
		t.Fatalf("selected string: got %s want n", got)
	}
	if got := Project([]int{4, 5}[1], KindScalar); got != "5" {
		t.Fatalf("index: got %s want 5", got)
	}
	if got := Project(true, KindScalar); got != "true" {
		t.Fatalf("bool: got %s want true", got)
	}
}

// Ordinary strings are never relabelled as JSON because of what they contain.
func TestProjectDoesNotLabelStringsByContent(t *testing.T) {
	for _, s := range []string{`{"a":1}`, `[1,2]`, "null", "7", "true"} {
		if got := Project(s, KindScalar); got != s {
			t.Fatalf("scalar %q became %q", s, got)
		}
	}
}

// Project reads only. A readonly binding's identity survives projection, and
// repeated projection is byte-identical.
func TestProjectReadsOnly(t *testing.T) {
	m := map[string]int{"a": 1, "b": 2}
	s := []int{1, 2, 3}
	h := projHolder{Item: projPair{First: "n", Second: 7}}
	beforeM := map[string]int{"a": 1, "b": 2}
	beforeS := []int{1, 2, 3}
	beforeH := h

	first := Project(m, KindObject) + Project(s, KindObject) + Project(&h, KindObject)
	second := Project(m, KindObject) + Project(s, KindObject) + Project(&h, KindObject)

	if first != second {
		t.Fatalf("projection is not deterministic:\n%s\n%s", first, second)
	}
	if !reflect.DeepEqual(m, beforeM) || !reflect.DeepEqual(s, beforeS) || h != beforeH {
		t.Fatalf("projection mutated its input")
	}
}

type projStringer struct{ N int }

func (projStringer) String() string { panic("projection must not call String") }

type projMarshaler struct{ N int }

func (projMarshaler) MarshalJSON() ([]byte, error) { panic("projection must not call MarshalJSON") }

// A map alias or struct carrying a caller method must not have that method run
// during projection, and must not leak a marshaling handle.
func TestProjectNeverRunsCallerMethods(t *testing.T) {
	for name, v := range map[string]any{
		"stringer":  projStringer{N: 1},
		"marshaler": projMarshaler{N: 1},
		"nested":    projHolderOf(projStringer{N: 1}),
	} {
		got := Project(v, KindObject)
		if got != InvalidObject {
			t.Fatalf("%s: got %s want the fixed invalid marker", name, got)
		}
	}
}

type projAny struct{ V any }

func projHolderOf(v any) projAny { return projAny{V: v} }

// Channels and callables are capabilities. They never leak a marshaling
// handle, a pointer, or any other referenceable token.
func TestProjectNeverLeaksCapabilities(t *testing.T) {
	ch := make(chan int)
	fn := func() {}
	for name, v := range map[string]any{
		"chan":        ch,
		"func":        fn,
		"nested chan": projAny{V: ch},
		"nested func": projAny{V: fn},
	} {
		if object := Project(v, KindObject); object != InvalidObject {
			t.Fatalf("%s object kind: got %s", name, object)
		}
		if _, err := ProjectErr(v, KindScalar); err == nil {
			t.Fatalf("%s scalar kind: expected an explicit failure", name)
		}
	}
}

// A cyclic graph terminates at the marker rather than recursing.
func TestProjectRejectsCycles(t *testing.T) {
	type node struct{ Next *node }
	n := &node{}
	n.Next = n
	if got := Project(n, KindObject); got != InvalidObject {
		t.Fatalf("cycle: got %s", got)
	}
}

// Floating values reaching the runtime scalar path fail explicitly. Measured
// against the engine, the only float rendering that exists is an untyped
// literal's exact rational, produced by the compiler from retained provenance;
// a float64 in hand cannot recover it, so there is nothing faithful to print.
func TestProjectFloatScalarFailsExplicitly(t *testing.T) {
	for _, v := range []any{1.5, float32(1.5), 1.1} {
		if _, err := ProjectErr(v, KindScalar); err == nil {
			t.Fatalf("float %v: expected an explicit failure", v)
		}
	}
}

// The failure is reported, not swallowed into a marker string that would look
// like a successful projection to everything downstream.
func TestProjectReportsFailureThroughFail(t *testing.T) {
	var errs strings.Builder
	oldErr, oldStatus := Stderr, Status
	Stderr, Status = &errs, 0
	defer func() { Stderr, Status = oldErr, oldStatus }()

	if got := Project(1.1, KindScalar); got != "" {
		t.Fatalf("got %q, want the empty string on failure", got)
	}
	if Status == 0 {
		t.Fatal("failed projection left status 0")
	}
	if !strings.Contains(errs.String(), "provenance") {
		t.Fatalf("failure not reported: %q", errs.String())
	}
}

// The generated program links this package, so Project is a standard-library
// port of the interpreter's coercion rather than a call into it. This test is
// what keeps the port honest: the two must agree byte for byte on every shape
// the compiler can route to KindObject. The expand import is test-only and so
// never reaches a generated program's module graph.
func TestProjectMatchesInterpreterCoercion(t *testing.T) {
	type tagged struct {
		Kept    int `json:"kept"`
		Skipped int `json:"-"`
		hidden  int
		Zeroed  int `json:"zeroed,omitzero"`
		Quoted  int `json:"quoted,string"`
	}
	type deep struct{ Next *deep }
	cyclic := &deep{}
	cyclic.Next = cyclic
	var nilPtr *projPair

	cases := []any{
		nil,
		projHolder{Item: projPair{First: "n", Second: 7}},
		&projHolder{Item: projPair{First: "n", Second: 7}},
		map[string]int{"b": 2, "a": 1},
		map[int]string{2: "b", 1: "a"},
		map[projPair]int{},
		[]int{1, 2, 3},
		[2]string{"a", "b"},
		[]byte("hi"),
		map[string]any{"s": "x", "b": true, "n": 1.5, "z": nil},
		tagged{Kept: 1, Skipped: 2, hidden: 3, Quoted: 4},
		"incremental",
		`{"a":1}`,
		7,
		true,
		1.5,
		nilPtr,
		[]any{make(chan int)},
		projStringer{N: 1},
		projMarshaler{N: 1},
		projAny{V: make(chan int)},
		cyclic,
		math.Inf(1),
		math.NaN(),
	}
	for i, v := range cases {
		want := expand.ObjectString(v)
		if got := Project(v, KindObject); got != want {
			t.Errorf("case %d (%T): shellrt %s, interpreter %s", i, v, got, want)
		}
	}
	_ = tagged{}.hidden
}

// --- Measured against the engine ------------------------------------------
//
// Every expectation below was observed by running the source through
// interp.Runner with interp.Lang(syntax.LangBashPP); the observed stdout is
// quoted in each comment. None of it is inferred from this implementation.

type projCount int

type projName string

type projFlag bool

// Named scalar types project plain, like their underlying kind. Observed:
// `type Count int; type Name string; type Flag bool; var c Count = 7;
// var n Name = "hi"; var f Flag = true; echo "c=$c n=$n f=$f"` → "c=7 n=hi f=true".
// A concrete type switch would have missed all three.
func TestProjectNamedScalarsArePlain(t *testing.T) {
	cases := map[any]string{
		projCount(7):    "7",
		projName("hi"):  "hi",
		projFlag(true):  "true",
		projCount(-3):   "-3",
		byte(65):        "65",
		rune(66):        "66",
		uint64(1 << 40): "1099511627776",
	}
	for v, want := range cases {
		got, err := ProjectErr(v, KindScalar)
		if err != nil {
			t.Fatalf("%T(%v): %v", v, v, err)
		}
		if got != want {
			t.Fatalf("%T(%v): got %q want %q", v, v, got, want)
		}
	}
}

type projMethodNamed int

func (projMethodNamed) String() string { panic("scalar projection must not call String") }

// A named scalar carrying a method still projects by kind, without running it.
func TestProjectNamedScalarIgnoresMethods(t *testing.T) {
	got, err := ProjectErr(projMethodNamed(7), KindScalar)
	if err != nil || got != "7" {
		t.Fatalf("got %q, %v; want 7", got, err)
	}
}

// Observed: `p := &s; echo "p=[$p]"` → "p=[]", and identically for &namedInt,
// new(S) and a nil *S. Corpus interfaces/assignment-assert-type-switch.bpp
// records `typed-nil-pointer:` for a typed nil pointer. Pointer roots are empty
// whether or not they are nil.
func TestProjectPointerRootsAreEmpty(t *testing.T) {
	s := projPair{First: "n", Second: 7}
	n := projCount(7)
	var nilPtr *projPair
	for name, v := range map[string]any{
		"&struct":     &s,
		"&namedInt":   &n,
		"new(struct)": new(projPair),
		"nil *struct": nilPtr,
	} {
		got, err := ProjectErr(v, KindPointer)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != "" {
			t.Fatalf("%s: got %q want the empty string", name, got)
		}
	}
}

// Observed: `var i S; echo "i=[$i]"` → "i=[]"; corpus
// interfaces/embedded-promotion-assertions.bpp records `nil-interface-assert::false`.
// And `var v C = 7; var i I = v; echo "i=[$i]"` → "i=[7]".
func TestProjectInterfaceRootIsNilCapable(t *testing.T) {
	var nilIface any
	got, err := ProjectErr(nilIface, KindInterface)
	if err != nil || got != "" {
		t.Fatalf("nil interface: got %q, %v; want empty", got, err)
	}
	var nilPtr *projPair
	if got, err := ProjectErr(any(nilPtr), KindInterface); err != nil || got != "" {
		t.Fatalf("interface holding typed nil: got %q, %v; want empty", got, err)
	}
	if got, err := ProjectErr(any(projCount(7)), KindInterface); err != nil || got != "7" {
		t.Fatalf("interface holding named scalar: got %q, %v; want 7", got, err)
	}
}

// Rich nil behaviour is unchanged by the pointer and interface kinds. Observed:
// `var m map[string]int; var s []int; echo "m=[$m] s=[$s]"` → "m=[null] s=[null]",
// and corpus pointers/address-deref-new.bpp ends "...:0:0:null:null".
func TestProjectRichNilRootsStayNull(t *testing.T) {
	var m map[string]int
	var sl []int
	if got := Project(m, KindObject); got != "null" {
		t.Fatalf("nil map: got %s want null", got)
	}
	if got := Project(sl, KindObject); got != "null" {
		t.Fatalf("nil slice: got %s want null", got)
	}
}

type projNamedSlice []int

type projNamedMap map[string]int

// Observed: `type L []int; type M map[string]int; l := L{1,2}; m := M{"a":1};
// echo "l=[$l] m=[$m]"` → `l=[[1,2]] m=[{"a":1}]`. Named composites stay JSON.
func TestProjectNamedCompositesAreJSON(t *testing.T) {
	if got := Project(projNamedSlice{1, 2}, KindObject); got != "[1,2]" {
		t.Fatalf("named slice: got %s", got)
	}
	if got := Project(projNamedMap{"a": 1}, KindObject); got != `{"a":1}` {
		t.Fatalf("named map: got %s", got)
	}
}

// Observed: `s := S{N:1, T:"x"}; echo "s=[$s]"` → `s=[{"N":1,"T":"x"}]`, and
// `a := [2]int{1,2}` → `a=[[1,2]]`.
func TestProjectStructAndArrayRoots(t *testing.T) {
	type projST struct {
		N int
		T string
	}
	if got := Project(projST{N: 1, T: "x"}, KindObject); got != `{"N":1,"T":"x"}` {
		t.Fatalf("struct root: got %s", got)
	}
	if got := Project([2]int{1, 2}, KindObject); got != "[1,2]" {
		t.Fatalf("array root: got %s", got)
	}
}
