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

func TestBashPPTupleAssignAtomicSemantics(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"swap uses old RHS values", "var x int = 1\nvar y int = 2\nx, y = y, x\nprintf '%s:%s' \"$x\" \"$y\"", "2:1"},
		{"duplicate LHS commits left to right", "var x int = 1\nx, x = 2, 3\nprintf '%s' \"$x\"", "3"},
		{"typed mismatch is atomic", "var x int = 1\nvar y bool = true\nx, y = 2, \"bad\"\nprintf '|%s|%s|' \"$x\" \"$y\"", "BASHPP-EASSIGN-TYPE: cannot assign String to bool\n|1|true|"},
		{"undeclared target is atomic", "var x int = 1\nx, missing = 2, 3\nprintf '|%s|%s|' \"$x\" \"${missing-unset}\"", "BASHPP-EASSIGN-UNDECLARED: assignment target missing is not declared\n|1|unset|"},
		{"arity is rejected", "var x int = 1\nvar y int = 2\nx, y = 3\nprintf '%s:%s' \"$x\" \"$y\"", "BASHPP-EASSIGN-ARITY: 2 variable(s) but 1 value(s)\n1:2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
			bashPPRun(t, r, "func main() {\n"+tc.body+"\n}\nmain()\n")
			qt.Assert(t, qt.Equals(out.String(), tc.want))
		})
	}
}

func TestBashPPPackageInitOrderPolicy(t *testing.T) {
	const src = "var first int = second\nvar second int = 2\n"
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP), interp.WithBashCompatErrors(true))
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "init.bpp")
	qt.Assert(t, qt.IsNil(err))
	err = r.Run(context.Background(), file)
	qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
	qt.Assert(t, qt.StringContains(out.String(), "init.bpp: line 1: BASHPP-EINIT-ORDER:"))
}

func TestBashPPTupleAssignFunctionResultsAtomic(t *testing.T) {
	const src = "func pair() (int, bool) { return 7, true }\nfunc main() {\nvar x int = 1\nvar y bool = false\nx, y = pair()\nprintf '%s:%s' \"$x\" \"$y\"\n}\nmain()\n"
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, src)
	qt.Assert(t, qt.Equals(out.String(), "7:true"))
}

func TestBashPPTupleAssignFunctionTypeFailureRollsBack(t *testing.T) {
	const src = "func pair() (int, bool) { return 7, true }\nfunc main() {\nvar x int = 1\nvar y int = 2\nx, y = pair()\nprintf '|%s:%s|' \"$x\" \"$y\"\n}\nmain()\n"
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, src)
	qt.Assert(t, qt.StringContains(out.String(), "BASHPP-EASSIGN-TYPE: cannot assign bool to int\n|1:2|"))
}

func TestBashPPInitFunctionIsExplicitlyUnsupported(t *testing.T) {
	const src = "func init() {\n}\n"
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP), interp.WithBashCompatErrors(true))
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "init.bpp")
	qt.Assert(t, qt.IsNil(err))
	err = r.Run(context.Background(), file)
	qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
	qt.Assert(t, qt.StringContains(out.String(), "init.bpp: line 1: BASHPP-EINIT-FUNC:"))
}
