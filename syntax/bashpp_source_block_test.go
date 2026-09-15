package syntax

import (
	"bytes"
	"strings"
	"testing"
	"testing/iotest"
)

func TestBashPPSourceBlockParsePrintWalkAndStreaming(t *testing.T) {
	src := "~~~~python as py\n\ndef add(a: int, b: int) -> int:\n    return a + b\n~~~~\npy.add(2, 3)\n"
	parse := func(rd interface{ Read([]byte) (int, error) }) *File {
		t.Helper()
		f, err := NewParser(Variant(LangBashPP)).Parse(rd, "polyglot.bpp")
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	buffered := parse(strings.NewReader(src))
	streamed := parse(iotest.OneByteReader(strings.NewReader(src)))
	for _, f := range []*File{buffered, streamed} {
		if len(f.Stmts) != 2 {
			t.Fatalf("statements = %d", len(f.Stmts))
		}
		block, ok := f.Stmts[0].Cmd.(*SourceBlock)
		if !ok {
			t.Fatalf("command = %T", f.Stmts[0].Cmd)
		}
		if block.Fence != "~~~~" || block.Language.Value != "python" || block.Alias.Value != "py" {
			t.Fatalf("block = %#v", block)
		}
		if block.Body != "\ndef add(a: int, b: int) -> int:\n    return a + b\n" {
			t.Fatalf("body = %q", block.Body)
		}
		if block.Pos().Line() != 1 || block.BodyPos.Line() != 2 || block.ClosingPos.Line() != 5 {
			t.Fatalf("positions: %v %v %v", block.Pos(), block.BodyPos, block.ClosingPos)
		}
		seen := false
		Walk(f, func(n Node) bool {
			if n == block {
				seen = true
			}
			return true
		})
		if !seen {
			t.Fatal("source block not walked")
		}
	}
	var out bytes.Buffer
	if err := NewPrinter().Print(&out, buffered); err != nil {
		t.Fatal(err)
	}
	if out.String() != src {
		t.Fatalf("printed:\n%s", out.String())
	}
}

func TestBashPPSourceBlockDialectIsolationAndNearMiss(t *testing.T) {
	valid := "~~~python\ndef f():\n    return 1\n~~~\n"
	f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(valid), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Stmts[0].Cmd.(*SourceBlock); !ok {
		t.Fatalf("command = %T", f.Stmts[0].Cmd)
	}
	for _, src := range []string{"~~python\n", "~~~python nope\n", "echo ~~~python\n", " ~~~python\n", "command ~~~python\n", "'~~~python'\n"} {
		f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(src), "")
		if err != nil {
			continue
		}
		Walk(f, func(n Node) bool {
			if _, ok := n.(*SourceBlock); ok {
				t.Fatalf("near miss claimed: %q", src)
			}
			return true
		})
	}
	for _, lang := range []LangVariant{LangBash, LangPOSIX} {
		f, err := NewParser(Variant(lang)).Parse(strings.NewReader(valid), "")
		if err != nil {
			continue
		}
		Walk(f, func(n Node) bool {
			if _, ok := n.(*SourceBlock); ok {
				t.Fatalf("%s claimed fence", lang)
			}
			return true
		})
	}
}

func TestBashPPSourceBlockUnclosed(t *testing.T) {
	_, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader("~~~python\ndef f(): pass\n"), "bad.bpp")
	if err == nil || !strings.Contains(err.Error(), "unclosed source block") {
		t.Fatalf("error = %v", err)
	}
}

func TestBashPPSourceBlockCRLFUsesParserNewlineContract(t *testing.T) {
	f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader("~~~python\r\ndef f():\r\n    return 1\r\n~~~\r\n"), "windows.bpp")
	if err != nil {
		t.Fatal(err)
	}
	block := f.Stmts[0].Cmd.(*SourceBlock)
	if block.Body != "def f():\n    return 1\n" || block.ClosingPos.Line() != 4 {
		t.Fatalf("body=%q closing=%v", block.Body, block.ClosingPos)
	}
}

func TestBashPPTypeScriptSourceBlockRegistersDirectCalls(t *testing.T) {
	source := "~~~typescript\nexport function answer(): number { return 42 }\nfunction greet(name: string): string { return name }\n~~~\nx := answer()\ny := greet(hello)\n"
	f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(source), "typescript.bpp")
	if err != nil {
		t.Fatal(err)
	}
	block := f.Stmts[0].Cmd.(*SourceBlock)
	if block.Language.Value != "typescript" || len(f.Stmts) != 3 {
		t.Fatalf("block=%#v statements=%d", block, len(f.Stmts))
	}
}

func TestBashPPRustSourceBlockRegistersDirectCalls(t *testing.T) {
	for _, language := range []string{"rust", "rs"} {
		src := "~~~" + language + "\npub fn add(a: i64, b: i64) -> i64 { a + b }\n~~~\nx := add(1, 2)\n"
		f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(src), "rust.bpp")
		if err != nil {
			t.Fatalf("%s: %v", language, err)
		}
		block, ok := f.Stmts[0].Cmd.(*SourceBlock)
		if !ok || block.Language.Value != language {
			t.Fatalf("%s: block = %#v", language, f.Stmts[0].Cmd)
		}
		decl, ok := f.Stmts[1].Cmd.(*BashPPShortDecl)
		if !ok || decl.Call == nil || decl.Call.Fun[0].Value != "add" {
			t.Fatalf("%s: call = %#v", language, f.Stmts[1].Cmd)
		}
	}
}

// `py` is an alias spelling of `python` (as `ts` is of `typescript`): the
// parser's def look-ahead must treat a naked `~~~py` block like `~~~python`,
// so a later zero-argument `answer()` parses as a direct call rather than a
// shell function header.
func TestBashPPSourceBlockPyAliasDirectCallLookahead(t *testing.T) {
	for _, lang := range []string{"python", "py"} {
		src := "~~~" + lang + "\ndef answer() -> int:\n    return 42\n~~~\nx := answer()\necho \"$x\"\n"
		f, err := NewParser(Variant(LangBashPP)).Parse(strings.NewReader(src), "polyglot.bpp")
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if len(f.Stmts) != 3 {
			t.Fatalf("%s: statements = %d", lang, len(f.Stmts))
		}
		block, ok := f.Stmts[0].Cmd.(*SourceBlock)
		if !ok || block.Language.Value != lang {
			t.Fatalf("%s: block = %#v", lang, f.Stmts[0].Cmd)
		}
		if _, ok := f.Stmts[1].Cmd.(*FuncDecl); ok {
			t.Fatalf("%s: answer() parsed as a shell function header", lang)
		}
	}
}
