// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"io"
	"strings"
	"testing"
)

func TestBashPPMethodGrammarRoundTrip(t *testing.T) {
	t.Parallel()
	const src = "func (v Count) Value(prefix string) int {\n\treturn v\n}\nfunc (p *Count) Pointer() {\n\treturn\n}\n(*Count).Pointer(p)\n"
	for _, rd := range []io.Reader{strings.NewReader(src), funcOneByteReader{strings.NewReader(src)}} {
		f, err := NewParser(Variant(LangBashPP)).Parse(rd, "method.bpp")
		if err != nil {
			t.Fatal(err)
		}
		value := f.Stmts[0].Cmd.(*BashPPFuncDecl)
		if value.Receiver == nil || value.Receiver.Pointer || value.Receiver.Name.Value != "v" || value.Receiver.RecvType.Value != "Count" {
			t.Fatalf("value receiver = %#v", value.Receiver)
		}
		pointer := f.Stmts[1].Cmd.(*BashPPFuncDecl)
		if pointer.Receiver == nil || !pointer.Receiver.Pointer || pointer.Receiver.RecvType.Value != "Count" {
			t.Fatalf("pointer receiver = %#v", pointer.Receiver)
		}
		expr := f.Stmts[2].Cmd.(*BashPPCall)
		if !expr.PointerMethodExpr || len(expr.Fun) != 2 || expr.Pos().Offset() != uint(strings.Index(src, "(*Count)")) {
			t.Fatalf("pointer method expression = %#v", expr)
		}
		var out strings.Builder
		if err := NewPrinter().Print(&out, f); err != nil || out.String() != src {
			t.Fatalf("print = %q, %v", out.String(), err)
		}
		seen := 0
		Walk(f, func(n Node) bool {
			if _, ok := n.(*BashPPReceiver); ok {
				seen++
			}
			return true
		})
		if seen != 2 {
			t.Fatalf("walk saw %d receivers", seen)
		}
	}
}

func TestBashPPMethodGrammarDiagnosticsAndIsolation(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"func () M() { return; }\n",
		"func (a, b T) M() { return; }\n",
		"func (r **T) M() { return; }\n",
	} {
		if _, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(src), ""); err == nil {
			t.Errorf("parsed malformed receiver %q", src)
		}
	}
	const valid = "func (v Count) M() {\n return\n}\n"
	for _, lang := range []LangVariant{LangBash, LangPOSIX} {
		if _, err := NewParser(Variant(lang)).Parse(strings.NewReader(valid), ""); err == nil {
			t.Errorf("classic %v accepted method declaration", lang)
		}
	}
}

func TestBashPPTypedReceiverValuesAndMethodValueAST(t *testing.T) {
	t.Parallel()
	const src = "var v Count = 7\nvar p *Count\nf := v.Show\n"
	f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	v := f.Stmts[0].Cmd.(*BashPPDecl)
	p := f.Stmts[1].Cmd.(*BashPPDecl)
	mv := f.Stmts[2].Cmd.(*BashPPShortDecl)
	if v.DeclType.Value != "Count" || p.DeclType.Value != "*Count" || len(mv.MethodValue) != 2 {
		t.Fatalf("typed declarations/method value = %#v %#v %#v", v, p, mv)
	}
}
