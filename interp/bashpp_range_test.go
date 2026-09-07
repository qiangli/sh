// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestBashPPRangeStringsIntegersAndGenericCollections(t *testing.T) {
	const src = `type Vec[T any] []T
func main() {
 text := "aé"
 for i, r := range text { printf 'string:%s:%s\n' "$i" "$r" }
 for i := range 3 { printf 'integer:%s\n' "$i" }
 for range -2 { echo unreachable }
 values := Vec[int]{7, 8}
 for i, v := range values { printf 'generic:%s:%s\n' "$i" "$v" }
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out,
		"string:0:97\nstring:1:233\n"+
			"integer:0\ninteger:1\ninteger:2\n"+
			"generic:0:7\ngeneric:1:8\n"))
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

func TestBashPPRangeControlFlowAndIterationLifetime(t *testing.T) {
	const src = `func stop() {
 for i := range 5 {
  if i == 0 { continue }
  printf 'loop:%s\n' "$i"
  if i == 2 { return }
 }
 echo unreachable
}
func main() {
 for i := range 2 { if i == 1 { break } }
 printf 'after:<%s>\n' "${i-unset}"
 stop()
 echo caller
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "after:<unset>\nloop:1\nloop:2\ncaller\n"))
}

func TestBashPPRangePreservesIterationTypes(t *testing.T) {
	const src = `type Count int
func takeCount(v Count) { printf 'count:%s\n' "$v" }
func takeRune(v rune) { printf 'rune:%s\n' "$v" }
func main() {
 var count Count = 2
 for i := range count {
  takeCount(i)
 }
 for _, r := range "é" {
  takeRune(r)
 }
 nested := [][]int{{7}}
 for _, values := range nested { printf 'nested:%s\n' values[0] }
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "count:0\ncount:1\nrune:233\nnested:7\n"))
}

func TestBashPPRangePositionedUndefinedAndFunctionDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"undefined", "for range missing { }", "fixture.bpp: line 2: BASHPP-ERANGE-TYPE: BASHPP-EEXPR-UNDEFINED: undefined: missing"},
		{"invalid scalar", "for range true { }", "fixture.bpp: line 2: BASHPP-ERANGE-TYPE: cannot range over Bool"},
		{"function rejected before execution", "f := func(yield func) { echo ran }\n for range f { }", "fixture.bpp: line 3: BASHPP-ERANGE-FUNC:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src := "func main() {\n" + test.body + "\n}\nmain()\n"
			var out strings.Builder
			r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP), interp.WithBashCompatErrors(true))
			f, parseErr := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "fixture.bpp")
			qt.Assert(t, qt.IsNil(parseErr))
			err := r.Run(context.Background(), f)
			qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
			qt.Assert(t, qt.StringContains(out.String(), test.want))
			if strings.Contains(out.String(), "ran\n") {
				t.Fatalf("rejected iterator executed: %q", out.String())
			}
		})
	}
}
