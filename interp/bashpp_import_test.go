package interp_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestBashPPStdlibImportSelectorCalls(t *testing.T) {
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP), interp.Env(expand.ListEnviron(os.Environ()...)))
	run := func(src string) {
		t.Helper()
		f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "source.bpp")
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(src, "Println(") {
			if _, ok := f.Stmts[0].Cmd.(*syntax.BashPPCall); !ok {
				t.Fatalf("dot call parsed as %T", f.Stmts[0].Cmd)
			}
		}
		if err := r.Run(context.Background(), f); err != nil {
			t.Fatal(err)
		}
	}
	run("import \"fmt\"\n")
	run("fmt.Println(\"incremental\")\n")
	r.Reset()
	run("import (\n\tf \"fmt\"\n\t_ \"embed\"\n)\n")
	run("f.Println(\"alias\")\n")
	r.Reset()
	run("import . \"fmt\"\n")
	run("Println(\"dot\")\n")
	if got := out.String(); got != "incremental\nalias\ndot\n" {
		t.Fatalf("output %q", got)
	}
}

func TestBashPPImportCancellation(t *testing.T) {
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP), interp.Env(expand.ListEnviron(os.Environ()...)))
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader("import \"fmt\"\nfmt.Println(\"no\")\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Run(ctx, f); err == nil {
		t.Fatal("expected cancellation")
	}
}
