// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

type bashppByteReader struct{ io.Reader }

func (r bashppByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}

func TestBashPPShortDeclSupportedShapes(t *testing.T) {
	shapes := []string{
		"x := 42", `x := "hello"`, "x, y := 1, 2",
		`m := map[string]int{"a": 1}`, "s := []int{1, 2, 3}",
		`g := Gopher{Name: "x"}`, "f := Max[int]",
		"x := f()", "x := f(1, 2)", `config, err := readConfig("config.json")`,
	}
	for _, src := range shapes {
		t.Run(src, func(t *testing.T) {
			for _, posix := range []bool{false, true} {
				for _, bytewise := range []bool{false, true} {
					var rd io.Reader = strings.NewReader(src)
					if bytewise {
						rd = bashppByteReader{rd}
					}
					f, err := NewParser(Variant(LangBashPP), PosixMode(posix)).Parse(rd, "shape.sh")
					if err != nil {
						t.Fatalf("posix=%v bytewise=%v: %v", posix, bytewise, err)
					}
					d, ok := f.Stmts[0].Cmd.(*BashPPShortDecl)
					if !ok {
						t.Fatalf("bytewise=%v: got %T", bytewise, f.Stmts[0].Cmd)
					}
					if d.Pos().Offset() != 0 || d.End().Offset() != uint(len(src)) {
						t.Fatalf("span [%d,%d), want [0,%d)", d.Pos().Offset(), d.End().Offset(), len(src))
					}
				}
			}
		})
	}
}

func TestBashPPScalarExpressionReachabilityIsGoRegionBounded(t *testing.T) {
	const src = "func main() { x := 1 + 2 }\n"
	for _, bytewise := range []bool{false, true} {
		var rd io.Reader = strings.NewReader(src)
		if bytewise {
			rd = bashppByteReader{rd}
		}
		f, err := NewParser(Variant(LangBashPP)).Parse(rd, "expr.sh")
		if err != nil {
			t.Fatalf("bytewise=%v: %v", bytewise, err)
		}
		fn := f.Stmts[0].Cmd.(*BashPPFuncDecl)
		d, ok := fn.Body.Stmts[0].Cmd.(*BashPPShortDecl)
		if !ok {
			t.Fatalf("bytewise=%v: got %T", bytewise, fn.Body.Stmts[0].Cmd)
		}
		if !d.GoRegion {
			t.Fatalf("bytewise=%v: scalar expression was not marked as committed Go", bytewise)
		}
		if got := bashppWordText(d.Rhs[0]); got != "1 + 2" {
			t.Fatalf("rhs = %q, want source expression", got)
		}
	}
}

func TestBashPPScalarExpressionDoesNotClaimTopLevelShellCommand(t *testing.T) {
	bashppCheckIdentical(t, "x := 1 + 2\n")
}

func TestBashPPGoCallsAndTerminators(t *testing.T) {
	for _, src := range []string{
		"f(1, 2)", "x.y.z()", "clear(m)",
		"f(1); cat", "f(1)\ncat",
	} {
		t.Run(src, func(t *testing.T) {
			f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(src), "call.sh")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			Walk(f, func(n Node) bool {
				if _, ok := n.(*BashPPCall); ok {
					found = true
				}
				return true
			})
			if !found {
				t.Fatalf("no BashPPCall in %T", f.Stmts[0].Cmd)
			}
		})
	}
}

func TestBashPPCallTerminatorGrammar(t *testing.T) {
	for _, tok := range []token{_EOF, _Newl, semicolon} {
		if !bashppCallTerminator(tok) {
			t.Errorf("published terminator %v was rejected", tok)
		}
	}
	for _, tok := range []token{
		and, or, orAnd, andAnd, orOr, andPipe, andBang,
		rdrOut, appOut, rdrIn, rdrInOut, dplIn, dplOut, rdrClob,
		rdrAll, appAll, rightParen,
	} {
		if bashppCallTerminator(tok) {
			t.Errorf("continuation %v was admitted as a call terminator", tok)
		}
	}
}

func TestBashPPParenFallbackIsExact(t *testing.T) {
	for _, src := range []string{
		"x.y() { echo ok; }", "x := f(1", "x := f(1 2)", "x := f(1,)",
		"f(1)>out", "f(1) >out", "x := f()>out", "x := f() >out",
		"f(1)<in", "f(1)>>out", "f(1)|&cat", "f(1)&&cat", "f(1)||cat",
		"f(1)& cat",
	} {
		t.Run(src, func(t *testing.T) {
			for _, posix := range []bool{false, true} {
				for _, bytewise := range []bool{false, true} {
					parse := func(lang LangVariant) (*File, error) {
						var rd io.Reader = strings.NewReader(src)
						if bytewise {
							rd = bashppByteReader{rd}
						}
						return NewParser(Variant(lang), PosixMode(posix)).Parse(rd, "fallback.sh")
					}
					bashFile, bashErr := parse(LangBash)
					ppFile, ppErr := parse(LangBashPP)
					if fmt.Sprint(ppErr) != fmt.Sprint(bashErr) {
						t.Fatalf("bytewise=%v diagnostic differs:\nbash: %v\npp:   %v", bytewise, bashErr, ppErr)
					}
					if !reflect.DeepEqual(ppFile, bashFile) {
						t.Fatalf("posix=%v bytewise=%v partial tree differs after rollback", posix, bytewise)
					}
				}
			}
		})
	}
}

// A pipe continues the command; it is not a statement terminator in the
// published Bash++ call grammar. The speculative call probe must therefore
// disappear completely, including with a reader that exposes one byte at a
// time, so the classic Bash tree and diagnostic remain byte-for-byte exact.
func TestBashPPCallBeforePipeFallsBackExactly(t *testing.T) {
	const src = "f(1)|cat"
	for _, posix := range []bool{false, true} {
		for _, bytewise := range []bool{false, true} {
			parse := func(lang LangVariant) (*File, error) {
				var rd io.Reader = strings.NewReader(src)
				if bytewise {
					rd = bashppByteReader{rd}
				}
				return NewParser(Variant(lang), PosixMode(posix)).Parse(rd, "pipe.sh")
			}
			bashFile, bashErr := parse(LangBash)
			ppFile, ppErr := parse(LangBashPP)
			if fmt.Sprint(ppErr) != fmt.Sprint(bashErr) {
				t.Fatalf("posix=%v bytewise=%v diagnostic differs:\nbash: %v\npp:   %v", posix, bytewise, bashErr, ppErr)
			}
			if !reflect.DeepEqual(ppFile, bashFile) {
				t.Fatalf("posix=%v bytewise=%v partial tree differs after rollback", posix, bytewise)
			}
			Walk(ppFile, func(n Node) bool {
				if _, ok := n.(*BashPPCall); ok {
					t.Fatalf("posix=%v bytewise=%v pipe prefix was claimed as BashPPCall", posix, bytewise)
				}
				return true
			})
		}
	}
}

func TestBashPPShortPorousShapesStayShell(t *testing.T) {
	for _, src := range []string{"x := x-y{}", "x := Max[]", "x := Max[int]junk"} {
		t.Run(src, func(t *testing.T) { bashppCheckIdentical(t, src) })
	}
}

func TestBashPPClassRArgumentsRemainUnevaluated(t *testing.T) {
	f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(`f("$x", $(side_effect), plain)`), "")
	if err == nil {
		t.Fatal("unsupported argument forms must roll back, not be evaluated or claimed")
	}
	f, err = NewParser(Variant(LangBashPP)).Parse(strings.NewReader(`f("literal", plain)`), "")
	if err != nil {
		t.Fatal(err)
	}
	call := f.Stmts[0].Cmd.(*BashPPCall)
	if _, ok := call.Args[0].Parts[0].(*DblQuoted); !ok {
		t.Fatalf("quoted argument was flattened to %T", call.Args[0].Parts[0])
	}
}
