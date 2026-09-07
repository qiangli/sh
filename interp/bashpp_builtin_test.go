// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
)

func TestBashPPPredeclaredCollectionBuiltins(t *testing.T) {
	src := `type Vec[T any] []T
func main() {
	 var nilSlice []int
	 empty := []int{}
	 made := make(Vec[int], 1, 3)
 alias := made
 grown := append(made, 2, 3)
 view := alias[:3]
 spread := append(nilSlice, grown...)
 copied := make([]int, 3)
 count := copy(copied, spread)
 m := make(map[string]int, 2)
 m["x"] = 7
 ma := m
 delete(ma, "x")
	 m["y"] = 8
	 clear(ma)
	 nilLen := len(nilSlice)
	 emptyLen := len(empty)
	 grownLen := len(grown)
	 grownCap := cap(grown)
	 mapLen := len(m)
	 strLen := len("é")
	 nilSliceOK := nilSlice == nil
	 emptyNil := empty == nil
	 madeNil := made == nil
	 mapNil := m == nil
	 print("lens:", nilLen, ":", emptyLen, ":", grownLen, ":", grownCap, " ")
	 println("copy", count, copied[2], "alias", view[2], "map", mapLen, "str", strLen, "nil", nilSliceOK, emptyNil, madeNil, mapNil)
	 clear(grown)
	 low := min(4, 2, 3)
	 high := max("a", "z", "m")
	 println("clear", view[0], view[1], view[2], "order", low, high)
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "lens:0:0:3:3 copy 3 3 alias 3 map 0 str 2 nil true false false false\nclear 0 0 0 order 2 z\n"))
}

func TestBashPPBuiltinDiagnosticsAndReadonly(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"len arity", "_ := len()", "BASHPP-EBUILTIN-ARITY:"},
		{"cap type", "_ := cap(1)", "BASHPP-EBUILTIN-TYPE:"},
		{"append type", "_ := append(1, 2)", "BASHPP-EBUILTIN-TYPE:"},
		{"copy type", "_ := copy(1, 2)", "BASHPP-EBUILTIN-TYPE:"},
		{"delete nil allowed", "var m map[string]int\n delete(m, \"x\")\n println(\"ok\")", ""},
		{"readonly clear", "s := []int{1}\n readonly s\n clear(s)", "BASHPP-EREADONLY-MUTATION:"},
		{"readonly append alias", "s := make([]int, 0, 1)\n readonly s\n _ := append(s, 1)", "BASHPP-EREADONLY-MUTATION:"},
		{"make negative", "n := -1\n _ := make([]int, n)", "BASHPP-EBUILTIN-SIZE:"},
		{"make arity", "_ := make()", "BASHPP-EBUILTIN-ARITY:"},
		{"new arity", "_ := new()", "BASHPP-EBUILTIN-TYPE:"},
		{"len untyped nil", "_ := len(nil)", "BASHPP-EBUILTIN-NIL:"},
		{"append untyped nil", "_ := append(nil, 1)", "BASHPP-EBUILTIN-NIL:"},
		{"min mixed", "_ := min(1, true)", "BASHPP-EBUILTIN-TYPE:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, stderr, err := runBashSharpCall(t, "func main() {\n"+test.body+"\n}\nmain()\n")
			if test.want == "" {
				qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
				return
			}
			qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
			if !strings.HasPrefix(stderr, test.want) {
				t.Fatalf("stderr %q does not start with %q", stderr, test.want)
			}
		})
	}
}
