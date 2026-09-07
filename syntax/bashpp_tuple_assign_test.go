// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestBashPPTupleAssignStreamingRoundTrip(t *testing.T) {
	for _, src := range []string{
		"func f() {\n\tx, y = y, x\n}\n",
		"func pair() (int, int) {\n\treturn 1, 2\n}\nfunc f() {\n\tx, y = pair()\n}\n",
	} {
		for _, wrap := range []func(io.Reader) io.Reader{
			func(r io.Reader) io.Reader { return r },
			func(r io.Reader) io.Reader { return iotest.OneByteReader(r) },
		} {
			file, err := NewParser(Variant(LangBashPP)).Parse(wrap(strings.NewReader(src)), "tuple.bpp")
			if err != nil {
				t.Fatal(err)
			}
			fn := file.Stmts[len(file.Stmts)-1].Cmd.(*BashPPFuncDecl)
			assign := fn.Body.Stmts[0].Cmd.(*BashPPAssign)
			if len(assign.Names) != 2 || (assign.Call == nil && (len(assign.Values) != 2 || len(assign.ValueExprs) != 2)) {
				t.Fatalf("tuple shape = %#v", assign)
			}
			var out strings.Builder
			if err := NewPrinter().Print(&out, file); err != nil {
				t.Fatal(err)
			}
			if out.String() != src {
				t.Fatalf("round trip = %q, want %q", out.String(), src)
			}
		}
	}
}

func TestBashPPTupleAssignClassicPOSIXIsolation(t *testing.T) {
	const src = "x, y = y, x\n"
	for _, lang := range []LangVariant{LangBash, LangPOSIX} {
		for _, wrap := range []func(io.Reader) io.Reader{
			func(r io.Reader) io.Reader { return r },
			func(r io.Reader) io.Reader { return iotest.OneByteReader(r) },
		} {
			file, err := NewParser(Variant(lang)).Parse(wrap(strings.NewReader(src)), "")
			if err != nil {
				t.Fatal(err)
			}
			Walk(file, func(node Node) bool {
				if _, ok := node.(*BashPPAssign); ok {
					t.Fatalf("%v claimed tuple assignment", lang)
				}
				return true
			})
		}
	}
}
