// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

const bashppInterfaceFixture = `type Shower interface { Show(int) string; Ptr() int }
func f() {
	x, ok := i.(Count)
	switch v := i.(type) {
	case nil:
		echo nil
	case Count:
		echo "$v"
	default:
		echo other
	}
}
`

func parseBashPPInterface(t *testing.T, rd io.Reader) *File {
	t.Helper()
	f, err := NewParser(Variant(LangBashPP)).Parse(rd, "interface.bpp")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestBashPPInterfaceASTStreamingWalkAndPrint(t *testing.T) {
	buffered := parseBashPPInterface(t, strings.NewReader(bashppInterfaceFixture))
	streamed := parseBashPPInterface(t, iotest.OneByteReader(strings.NewReader(bashppInterfaceFixture)))
	if !reflect.DeepEqual(buffered, streamed) {
		t.Fatalf("buffered and one-byte trees differ:\n%#v\n%#v", buffered, streamed)
	}
	decl := buffered.Stmts[0].Cmd.(*BashPPDecl)
	iface, ok := decl.DeclTypeExpr.(*BashPPInterfaceType)
	if !ok || len(iface.Methods) != 2 || iface.Methods[0].Name.Value != "Show" || iface.Methods[1].Name.Value != "Ptr" {
		t.Fatalf("interface type = %#v", decl.DeclTypeExpr)
	}
	fn := buffered.Stmts[1].Cmd.(*BashPPFuncDecl)
	if _, ok := fn.Body.Stmts[0].Cmd.(*BashPPShortDecl).Expr.(*BashPPTypeAssertExpr); !ok {
		t.Fatalf("type assertion = %#v", fn.Body.Stmts[0].Cmd)
	}
	sw := fn.Body.Stmts[1].Cmd.(*BashPPSwitch)
	if !sw.TypeSwitch || len(sw.Arms) != 3 {
		t.Fatalf("type switch = %#v", sw)
	}
	seen := map[string]bool{}
	Walk(buffered, func(n Node) bool {
		if n != nil {
			seen[reflect.TypeOf(n).String()] = true
		}
		return true
	})
	for _, want := range []string{"*syntax.BashPPInterfaceType", "*syntax.BashPPMethodSpec", "*syntax.BashPPTypeAssertExpr"} {
		if !seen[want] {
			t.Fatalf("Walk missed %s in %v", want, seen)
		}
	}
	var printed bytes.Buffer
	if err := NewPrinter().Print(&printed, buffered); err != nil {
		t.Fatal(err)
	}
	if printed.String() != bashppInterfaceFixture {
		t.Fatalf("print = %q, want %q", printed.String(), bashppInterfaceFixture)
	}
	reparsed := parseBashPPInterface(t, strings.NewReader(printed.String()))
	if !reflect.DeepEqual(buffered, reparsed) {
		t.Fatal("parse/print/reparse changed interface tree")
	}
}

func TestBashPPInterfaceFallbackBoundaries(t *testing.T) {
	for _, src := range []string{
		"type I interface { M(int) string }\n",
		"func f() {\n\tx, ok := i.(T)\n}\n",
		"func f() {\n\tswitch v := i.(type) { default: echo d }\n}\n",
	} {
		for _, lang := range []LangVariant{LangBash, LangPOSIX} {
			f, _ := NewParser(Variant(lang), RecoverErrors(8)).Parse(strings.NewReader(src), "")
			if f != nil {
				Walk(f, func(n Node) bool {
					switch n.(type) {
					case *BashPPInterfaceType, *BashPPTypeAssertExpr:
						t.Fatalf("%v produced interface AST for %q", lang, src)
					}
					return true
				})
			}
		}
	}
}
