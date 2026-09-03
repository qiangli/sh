// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package typedjson_test

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
	"mvdan.cc/sh/v3/syntax/typedjson"
)

// TestBashPPDeclRoundTrip covers the Bash++ nodes on the JSON surface, which
// wiring the parser dispatch made reachable for the first time.
//
// It is here because the reachability itself was the bug. Encoding is
// reflective and needs no registration, so a Bash++ node reached the encoder
// the moment LangBashPP could produce one — and turned up two failures that
// only a tree containing one can show:
//
//   - a node field named Type collides with the "Type" discriminator this
//     package puts first in every tagged object, and panics reflect.StructOf.
//     That is why [syntax.BashPPDecl] spells its field DeclType.
//   - this package encodes small enums by their string form so the wire format
//     survives new values, and decodes them through TextUnmarshaler — which
//     had only ever been needed for uint32 tokens, so a uint8 enum encoded
//     cleanly and then panicked on the way back in.
//
// Both are invisible to a unit test that builds a node directly and never
// serializes it, and both would have surfaced as a `shfmt --tojson` panic.
func TestBashPPDeclRoundTrip(t *testing.T) {
	t.Parallel()

	for _, src := range []string{
		"var x = 1\n",
		"const K = 2\n",
		"type T int\n",
		"type ID = string\n",
		"echo before\nvar x = 1\necho after\n",
		"if true; then var x = 1; fi\n",
	} {
		t.Run(src, func(t *testing.T) {
			t.Parallel()
			parser := syntax.NewParser(syntax.Variant(syntax.LangBashPP))
			f, err := parser.Parse(strings.NewReader(src), "")
			if err != nil {
				t.Fatal(err)
			}

			var enc strings.Builder
			if err := typedjson.Encode(&enc, f); err != nil {
				t.Fatalf("encoding: %v", err)
			}
			if !strings.Contains(enc.String(), `"Type":"BashPPDecl"`) {
				t.Fatalf("encoded form does not carry the node's type tag:\n%s", enc.String())
			}

			node, err := typedjson.Decode(strings.NewReader(enc.String()))
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}
			var out strings.Builder
			if err := syntax.NewPrinter().Print(&out, node); err != nil {
				t.Fatalf("printing the decoded tree: %v", err)
			}
			if out.String() != src {
				t.Fatalf("round trip produced %q, want %q", out.String(), src)
			}
		})
	}
}

func TestBashPPFormsRoundTrip(t *testing.T) {
	for _, src := range []string{"x := 42\n", "x, y := 1, 2\n", "f(1, 2)\n", "x.y.z()\n", "import f \"fmt\"\n", "import (\n\tf \"fmt\"\n\t_ \"embed\"\n)\n"} {
		t.Run(src, func(t *testing.T) {
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
			if err := syntax.NewPrinter().Print(&out, node); err != nil || out.String() != src {
				t.Fatalf("print %q: %v", out.String(), err)
			}
		})
	}
}
