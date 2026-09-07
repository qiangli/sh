// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package shellrt

import (
	"math"
	"reflect"
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

// Channels and callables are capabilities. They collapse to the fixed marker
// with no handle of any kind in the output.
func TestProjectNeverLeaksCapabilities(t *testing.T) {
	ch := make(chan int)
	fn := func() {}
	for name, v := range map[string]any{
		"chan":        ch,
		"func":        fn,
		"nested chan": projAny{V: ch},
		"nested func": projAny{V: fn},
		"scalar chan": ch,
		"scalar func": fn,
	} {
		object := Project(v, KindObject)
		if object != InvalidObject {
			t.Fatalf("%s object kind: got %s", name, object)
		}
		if scalar := Project(v, KindScalar); scalar != UnsupportedScalar {
			t.Fatalf("%s scalar kind: got %s", name, scalar)
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

// Floating values reaching the runtime scalar path fail closed rather than
// producing a decimal the interpreter would never print.
func TestProjectFloatScalarFailsClosed(t *testing.T) {
	for _, v := range []any{1.5, float32(1.5), 1.1} {
		if got := Project(v, KindScalar); got != UnsupportedScalar {
			t.Fatalf("float %v: got %s want %s", v, got, UnsupportedScalar)
		}
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
