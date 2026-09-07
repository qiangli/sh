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
