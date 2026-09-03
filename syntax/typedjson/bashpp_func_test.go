// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package typedjson_test

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
	"mvdan.cc/sh/v3/syntax/typedjson"
)

func TestBashPPFuncRoundTrip(t *testing.T) {
	const src = "func f(a, b int) (x, y int) {\n\tdefer g(a)\n\tx = a\n\ty = b\n\treturn\n}\n"
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	var enc strings.Builder
	if err := typedjson.Encode(&enc, f); err != nil {
		t.Fatal(err)
	}
	node, err := typedjson.Decode(strings.NewReader(enc.String()))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := syntax.NewPrinter().Print(&out, node); err != nil {
		t.Fatal(err)
	}
	if out.String() != src {
		t.Fatalf("round trip = %q, want %q", out.String(), src)
	}
}
