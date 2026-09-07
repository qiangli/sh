// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"strings"
	"testing"
)

func TestBashPPGenericComparableNamedTypes(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		init string
		want string
	}{
		{"named slice", "type Values []int", "Values{1}", "BASHPP-EGENERIC-CONSTRAINT:"},
		{"named map", "type Values map[string]int", "Values{\"a\": 1}", "BASHPP-EGENERIC-CONSTRAINT:"},
		{"struct containing slice", "type Values struct { Items []int }", "Values{Items: []int{1}}", "BASHPP-EGENERIC-CONSTRAINT:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.typ + "\nfunc accept[T comparable](v T) { echo accepted }\nv := " + tc.init + "\naccept(v)\n"
			if got := runBashPPFunc(t, src); !strings.Contains(got, tc.want) {
				t.Fatalf("output = %q, want to contain %q", got, tc.want)
			}
		})
	}
}

func TestBashPPGenericComparableNamedTypesAccepted(t *testing.T) {
	tests := []struct {
		typ  string
		init string
	}{
		{"type Values [2]int", "Values{1, 2}"},
		{"type Values struct { Count int; Name string }", "Values{Count: 1, Name: \"n\"}"},
	}
	for _, tc := range tests {
		src := tc.typ + "\nfunc accept[T comparable](v T) { echo ok }\nfunc main() {\n v := " + tc.init + "\n accept(v)\n}\nmain()\n"
		if got := runBashPPFunc(t, src); got != "ok\n" {
			t.Fatalf("output = %q, want %q", got, "ok\\n")
		}
	}
}

func TestBashPPGenericMultipleInferenceAndNamedConstraint(t *testing.T) {
	const src = `type Stringer interface { String() string }
type Name string
func (v Name) String() string { return v }
func choose[A Stringer, B any](a A, b B) B { return b }
func main() {
 var n Name = "n"
 x := choose(n, 7)
 echo "$x"
}
main()
`
	if got := runBashPPFunc(t, src); got != "7\n" {
		t.Fatalf("output = %q, want %q", got, "7\\n")
	}
}

func TestBashPPGenericNamedTypeRuntime(t *testing.T) {
	const src = `type Box[T any] struct { Value T }
func main() {
 var b Box[int] = Box[int]{Value:7}
 echo "$b"
}
main()
`
	if got := runBashPPFunc(t, src); !strings.Contains(got, `"Value":7`) {
		t.Fatalf("output = %q, want Box value", got)
	}
}

func TestBashPPGenericNamedTypeRecursiveSubstitution(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"nested named composite substitution",
			"type Pair[A, B any] struct { First A; Second B }\ntype Holder[T any] struct { Item Pair[string,T] }\nfunc main() {\n var h Holder[int] = Holder[int]{Item: Pair[string,int]{First:\"n\", Second:7}}\n echo \"$h\"\n}\nmain()\n",
			`"Second":7`,
		},
		{
			"recursive substitution arity rejection",
			"type Pair[A, B any] struct { First A; Second B }\ntype Holder[T any] struct { Item Pair[string] }\n",
			"BASHPP-EGENERIC-ARITY: Pair expects 2 type argument(s); got 1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runBashPPFunc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("output = %q, want to contain %q", got, tc.want)
			}
		})
	}
}

func TestBashPPGenericTypeSetConstraints(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"approximation accepts named underlying int",
			"type Age int\nfunc id[T ~int\\|string](v T) T {\n return v\n}\nvar a Age = 9\nx := id(a)\necho \"$x\"\n",
			"9\n",
		},
		{
			"union accepts string",
			"func id[T ~int\\|string](v T) T {\n return v\n}\nx := id[string](hi)\necho \"$x\"\n",
			"hi\n",
		},
		{
			"union rejects bool",
			"func id[T ~int\\|string](v T) T {\n return v\n}\nid[bool](true)\n",
			"BASHPP-EGENERIC-CONSTRAINT:",
		},
		{
			"named type arity",
			"type Box[T any] struct { Value T }\nvar b Box = Box[int]{Value:1}\n",
			"BASHPP-EGENERIC-ARITY: Box expects 1 type argument(s); got 0",
		},
		{
			"named type constraint",
			"type Box[T comparable] struct { Value T }\nvar xs []int = []int{1}\nvar b Box[[]int]\n",
			"BASHPP-EGENERIC-CONSTRAINT:",
		},
		{
			"recursive generic value cycle",
			"type Bad[T any] Bad[T]\n",
			"cyclic type declaration: Bad",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runBashPPFunc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("output = %q, want to contain %q", got, tc.want)
			}
		})
	}
}

func TestBashPPGenericNamedTypeMethodSets(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			"value method on instantiated named type",
			"type Box[T any] struct { Value T }\nfunc (b Box) Show() {\n echo show\n}\nfunc main() {\n var b Box[int] = Box[int]{Value:1}\n b.Show()\n}\nmain()\n",
			"show\n",
		},
		{
			"pointer method through addressable instantiated value",
			"type Box[T any] struct { Value T }\nfunc (b *Box) Touch() {\n echo touch\n}\nfunc main() {\n var b Box[int] = Box[int]{Value:1}\n b.Touch()\n}\nmain()\n",
			"touch\n",
		},
		{
			"pointer method not in value method expression set",
			"type Box[T any] struct { Value T }\nfunc (b *Box) Touch() {\n echo touch\n}\nfunc main() {\n var b Box[int] = Box[int]{Value:1}\n Box.Touch(b)\n}\nmain()\n",
			"Box.Touch is not in the method set of Box",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runBashPPFunc(t, tc.src)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("output = %q, want to contain %q", got, tc.want)
			}
		})
	}
}
