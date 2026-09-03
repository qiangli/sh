// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"io"
	"strings"
	"testing"
)

type funcOneByteReader struct{ io.Reader }

func (r funcOneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestBashPPFuncProbe(t *testing.T) {
	for _, src := range []string{
		"func f(a int, b string) int {\n return a\n}\n",
		"func f(a, b int) (x, y int) {\n x = a\n y = b\n return x, y\n}\n",
		"func f() {\n defer g(1)\n return\n}\n",
	} {
		f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(src), "")
		if err != nil {
			t.Errorf("parse %q: %v", src, err)
			continue
		}
		var out strings.Builder
		if err := NewPrinter().Print(&out, f); err != nil {
			t.Errorf("print %q: %v", src, err)
			continue
		}
		f2, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(out.String()), "")
		if err != nil {
			t.Errorf("reparse %q: %v", out.String(), err)
			continue
		}
		if _, ok := f2.Stmts[0].Cmd.(*BashPPFuncDecl); !ok {
			t.Fatalf("reparsed node is %T, want BashPPFuncDecl", f2.Stmts[0].Cmd)
		}
	}
}

func TestBashPPFuncOneByteAndDialectFallback(t *testing.T) {
	const src = "func f(a int) int {\n\treturn a\n}\n"
	for _, rd := range []io.Reader{strings.NewReader(src), funcOneByteReader{strings.NewReader(src)}} {
		f, err := NewParser(Variant(LangBashPP)).Parse(rd, "func.bpp")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := f.Stmts[0].Cmd.(*BashPPFuncDecl); !ok {
			t.Fatalf("got %T", f.Stmts[0].Cmd)
		}
	}
	for _, lang := range []LangVariant{LangBash, LangPOSIX} {
		_, err := NewParser(Variant(lang)).Parse(strings.NewReader(src), "func.sh")
		if err == nil {
			t.Fatalf("%v unexpectedly accepted Bash++ function", lang)
		}
	}
	if _, err := NewParser(Variant(LangBashPP), PosixMode(true)).Parse(strings.NewReader(src), "func.sh"); err != nil {
		t.Fatalf("POSIX parser should preserve the AST for runtime gating: %v", err)
	}
}
