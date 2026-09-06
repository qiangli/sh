// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
)

func TestBashPPCollectionsReadAndMutate(t *testing.T) {
	src := `func main() {
 a := [3]int{1, 2: 7}
 b := [...]string{1: "x", "y"}
 s := []int{1, 2}
 m := map[string][]int{"a": {4, 5}}
 printf '%s:%s:%s:%s:%s\n' a[0] a[2] b[1] b[2] m["a"][1]
 a[1] = 9
 s[0] = 8
 m["a"][0] = 6
 m["z"] = []int{3}
 missing := m["missing"]
 printf '%s:%s:%s:%s:%s\n' a[1] s[0] m["a"][0] m["z"][0] missing
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "1:7:x:y:5\n9:8:6:3:[]\n"))
}

func TestBashPPCollectionAssignmentValueAndReferenceSemantics(t *testing.T) {
	src := `func main() {
 a := [1][]int{{1}}
 b := a
 b[0] = []int{2}
 s := []int{3}
 t := s
 t[0] = 4
 m := map[string]int{"x": 5}
 n := m
 n["x"] = 6
 printf '%s:%s:%s:%s:%s:%s\n' a[0][0] b[0][0] s[0] t[0] m["x"] n["x"]
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "1:2:4:4:6:6\n"))
}

func TestBashPPCollectionDiagnostics(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"length", `x := [999999999999999999999999]int{1}`, "BASHPP-ECOLLECTION-LENGTH:"},
		{"element", `x := []int{"bad"}`, "BASHPP-ECOLLECTION-ELEMENT:"},
		{"map element", `x := map[string]int{1}`, "BASHPP-ECOLLECTION-MAP-ELEMENT:"},
		{"map key", `x := map[[]int]int{}`, "BASHPP-ECOLLECTION-KEY:"},
		{"duplicate index", `x := []int{0: 1, 0: 2}`, "BASHPP-ECOLLECTION-DUPLICATE:"},
		{"duplicate key", `x := map[string]int{"a": 1, "a": 2}`, "BASHPP-ECOLLECTION-DUPLICATE:"},
		{"literal bounds", `x := [1]int{1: 2}`, "BASHPP-ECOLLECTION-BOUNDS:"},
		{"read bounds", "func main() {\n x := []int{1}\n y := x[2]\n}\nmain()", "BASHPP-ECOLLECTION-BOUNDS:"},
		{"assign bounds", "func main() {\n x := []int{1}\n x[2] = 3\n}\nmain()", "BASHPP-ECOLLECTION-BOUNDS:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, stderr, err := runBashSharpCall(t, test.src+"\n")
			qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
			if !strings.HasPrefix(stderr, test.want) {
				t.Fatalf("stderr %q does not start with %q", stderr, test.want)
			}
		})
	}
}
