// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
)

func TestBashPPRangeStringsIntegersAndGenericCollections(t *testing.T) {
	const src = `type Vec[T any] []T
func sequence(yield func) {
 yield(10)
 yield(20)
}
func main() {
 text := "aé"
 for i, r := range text { printf 'string:%s:%s\n' "$i" "$r" }
 for i := range 3 { printf 'integer:%s\n' "$i" }
 for range -2 { echo unreachable }
 values := Vec[int]{7, 8}
 for i, v := range values { printf 'generic:%s:%s\n' "$i" "$v" }
 for v := range sequence { printf 'function:%s\n' "$v" }
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out,
		"string:0:97\nstring:1:233\n"+
			"integer:0\ninteger:1\ninteger:2\n"+
			"generic:0:7\ngeneric:1:8\n"+
			"function:10\nfunction:20\n"))
}

func TestBashPPRangeScalarDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"integer arity", "for i, v := range 2 {}", "BASHPP-ERANGE-ARITY:"},
		{"bool", "for range true {}", "BASHPP-ERANGE-TYPE:"},
		{"float", "for range 1.5 {}", "BASHPP-ERANGE-TYPE:"},
		{"function signature", "f := func(y int) {}\n for range f {}", "BASHPP-ERANGE-FUNC:"},
		{"function yield arity", "f := func(yield func) {\n yield(1)\n }\n for a, b := range f {}", "BASHPP-ERANGE-ARITY:"},
		{"function yield after false", "f := func(yield func) {\n yield(1)\n yield(2)\n }\n for v := range f { break }", "BASHPP-ERANGE-FUNC:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, stderr, err := runBashSharpCall(t, "func main() {\n"+test.body+"\n}\nmain()\n")
			qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
			if !strings.HasPrefix(stderr, test.want) {
				t.Fatalf("stderr %q does not start with %q", stderr, test.want)
			}
		})
	}
}
