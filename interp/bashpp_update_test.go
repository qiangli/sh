// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestBashPPCompoundAndIncDecRuntime(t *testing.T) {
	const src = `type Box struct { N int }
func main() {
 var x int = 5
 x += 3
 x *= 2
 x--
 var text string = "a"
 text += "b"
 values := []int{2}
 i := 0
 values[i]+=3
 box := Box{N: 4}
 box.N++
 p := &x
 *p += 2
 printf '%s:%s:%s:%s' "$x" "$text" values[0] box.N
}
main()
`
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, src)
	qt.Assert(t, qt.Equals(out.String(), "17:ab:5:5"))
}

func TestBashPPUpdateDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"typed wrap", "var x uint8 = 255\nx++\nprintf '|%s|' \"$x\"", "|0|"},
		{"const", "const x int = 1\nx++", "BASHPP-EUPDATE-TARGET:"},
		{"readonly", "var x int = 1\nreadonly x\nx += 1", "BASHPP-EUPDATE-WRITE:"},
		{"bad target", "missing.field += 1", "BASHPP-EUPDATE-TARGET:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP), interp.WithBashCompatErrors(true))
			bashPPRun(t, r, "func main() {\n"+tc.body+"\n}\nmain()\n")
			qt.Assert(t, qt.StringContains(out.String(), tc.want))
			if tc.name != "typed wrap" {
				qt.Assert(t, qt.StringContains(out.String(), "line "))
			}
		})
	}
}

func TestBashPPCompoundOperatorMatrix(t *testing.T) {
	for _, tc := range []struct {
		initial string
		op      string
		rhs     string
		want    string
	}{
		{"6", "+=", "2", "8"}, {"6", "-=", "2", "4"},
		{"6", "*=", "2", "12"}, {"6", "/=", "2", "3"},
		{"7", "%=", "4", "3"}, {"6", "&=", "3", "2"},
		{"4", "|=", "3", "7"}, {"6", "^=", "3", "5"},
		{"3", "<<=", "2", "12"}, {"12", ">>=", "2", "3"},
		{"7", "&^=", "2", "5"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			var out strings.Builder
			r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
			bashPPRun(t, r, "func main() {\nvar x int = "+tc.initial+"\nx"+tc.op+tc.rhs+"\nprintf '%s' \"$x\"\n}\nmain()\n")
			qt.Assert(t, qt.Equals(out.String(), tc.want))
		})
	}
}
