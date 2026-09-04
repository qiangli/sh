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

func runBashSharpCall(t *testing.T, src string) (stdout, stderr string, err error) {
	t.Helper()
	f, parseErr := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "fixture.bpp")
	qt.Assert(t, qt.IsNil(parseErr))
	var out, errs strings.Builder
	r := bashPPRunner(t, &out, interp.StdIO(nil, &out, &errs), interp.Lang(syntax.LangBashPP))
	err = r.Run(context.Background(), f)
	return out.String(), errs.String(), err
}

func TestBashPPKwargsDefaultsFixtures(t *testing.T) {
	positives := []struct{ name, src, want string }{
		{"reordered", "func greet(name string, retries int) {\n printf '%s:%d\\n' name retries\n}\ngreet(retries: 3, name: \"Ada\")\n", "Ada:3\n"},
		{"positional then named", "func pair(left string, right string) {\n printf '%s/%s\\n' left right\n}\npair(\"ordinary\", right: \"named\")\n", "ordinary/named\n"},
		{"omitted default", "func greet(name string, retries int = 3) {\n printf '%s:%d\\n' name retries\n}\ngreet(\"Ada\")\n", "Ada:3\n"},
		{"explicit default override", "func greet(name string, retries int = 3) {\n printf '%s:%d\\n' name retries\n}\ngreet(\"Ada\", 5)\n", "Ada:5\n"},
		{"named default override", "func greet(name string, retries int = 3) {\n printf '%s:%d\\n' name retries\n}\ngreet(\"Ada\", retries: 7)\n", "Ada:7\n"},
	}
	for _, tc := range positives {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, err := runBashSharpCall(t, tc.src)
			qt.Assert(t, qt.Equals(out, tc.want))
			qt.Assert(t, qt.Equals(stderr, ""))
			qt.Assert(t, qt.IsNil(err))
		})
	}

	errors := []struct{ name, src, want string }{
		{"unknown", "func greet(name string) {}\ngreet(who: \"Ada\")\n", "BASHPP-EKWARG-UNKNOWN: greet has no parameter named \"who\"\n"},
		{"duplicate name", "func greet(name string) {}\ngreet(name: \"Ada\", name: \"Grace\")\n", "BASHPP-EKWARG-DUPLICATE: argument \"name\" is supplied more than once\n"},
		{"duplicate binding", "func greet(name string) {}\ngreet(\"Ada\", name: \"Grace\")\n", "BASHPP-EARG-DUPLICATE-BINDING: parameter \"name\" is supplied positionally and by name\n"},
		{"kwargs missing", "func greet(name string, retries int) {}\ngreet(name: \"Ada\")\n", "BASHPP-EARG-MISSING: greet requires argument \"retries\"\n"},
		{"defaults missing", "func greet(name string, retries int = 3) {}\ngreet()\n", "BASHPP-EARG-MISSING: greet requires argument \"name\"\n"},
		{"too many", "func greet(name string, retries int = 3) {}\ngreet(\"Ada\", 3, 4)\n", "BASHPP-EARG-COUNT: greet accepts at most 2 arguments; got 3\n"},
		{"nontrailing default", "func greet(retries int = 3, name string) {}\n", "BASHPP-EDEFAULT-ORDER: required parameter \"name\" follows a default parameter\n"},
	}
	for _, tc := range errors {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, err := runBashSharpCall(t, tc.src)
			qt.Assert(t, qt.Equals(out, ""))
			qt.Assert(t, qt.Equals(stderr, tc.want))
			qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
		})
	}
}
