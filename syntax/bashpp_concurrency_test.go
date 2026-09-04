// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"strings"
	"testing"
)

func TestBashPPGoCallAndClassicIsolation(t *testing.T) {
	const src = "go worker(1, 2)\n"
	f, err := bashppParse(LangBashPP, src)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Stmts[0].Cmd.(*BashPPGo); !ok {
		t.Fatalf("got %T, want *BashPPGo", f.Stmts[0].Cmd)
	}
	got, err := bashppPrint(f)
	if err != nil {
		t.Fatal(err)
	}
	if got != src {
		t.Fatalf("print = %q, want %q", got, src)
	}

	// Parentheses, rather than the word go, are the Class-R disambiguator.
	// This remains the Go toolchain command in every dialect.
	for _, lang := range []LangVariant{LangBash, LangPOSIX, LangBashPP} {
		f, err := bashppParse(lang, "go build ./...\nfoo <- bar\n")
		if err != nil {
			t.Fatalf("%v: %v", lang, err)
		}
		for _, stmt := range f.Stmts {
			if _, ok := stmt.Cmd.(*CallExpr); !ok {
				t.Fatalf("%v: got %T, want shell CallExpr", lang, stmt.Cmd)
			}
		}
	}
}

func TestBashPPGoClosureAndEmptySelectRoundTrip(t *testing.T) {
	for _, src := range []string{"go func(x int) {\n\techo $x\n}(1)\n", "func f() {\n\tselect {}\n}\n"} {
		f, err := bashppParse(LangBashPP, src)
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		var out strings.Builder
		if err := NewPrinter().Print(&out, f); err != nil {
			t.Fatal(err)
		}
		if _, err := bashppParse(LangBashPP, out.String()); err != nil {
			t.Fatalf("reparse %q: %v", out.String(), err)
		}
	}
}
