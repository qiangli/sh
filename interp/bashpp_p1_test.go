// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// These tests construct the P1 nodes DIRECTLY rather than parsing source,
// because nothing can parse them yet: the command-position dispatch in
// parser.go is held behind the certification freeze. Building the nodes by
// hand is what lets the evaluation be tested before the wiring exists, so that
// when the wiring lands it is one call rather than a call plus a design.

func bashPPRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New(Lang(syntax.LangBashPP), StdIO(nil, nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	r.Reset()
	return r
}

func lit(s string) *syntax.Lit { return &syntax.Lit{Value: s} }

func word(s string) *syntax.Word {
	return &syntax.Word{Parts: []syntax.WordPart{lit(s)}}
}

func TestBashPPDeclare(t *testing.T) {
	t.Parallel()
	r := bashPPRunner(t)
	r.bashPPDeclare(context.Background(), &syntax.BashPPDecl{
		Site: syntax.StartVar,
		Kw:   lit("var"),
		Name: lit("x"),
		Init: []*syntax.Word{word("42")},
	})
	if got := r.lookupVar("x").Str; got != "42" {
		t.Fatalf("x = %q, want %q", got, "42")
	}
	// A scalar must stay a STRING, not become an object. If this flips, every
	// interpolation, arithmetic context and external command has to learn to
	// unwrap, which is the cost the design declined to pay for scalars.
	if k := r.lookupVar("x").Kind; k != expand.String {
		t.Fatalf("x kind = %v, want expand.String", k)
	}
}

func TestBashPPDeclareBare(t *testing.T) {
	t.Parallel()
	r := bashPPRunner(t)
	r.bashPPDeclare(context.Background(), &syntax.BashPPDecl{
		Site:     syntax.StartVar,
		Kw:       lit("var"),
		Name:     lit("x"),
		DeclType: lit("int"),
	})
	if got := r.lookupVar("x").Str; got != "" {
		t.Fatalf("bare declaration gave %q, want the empty string", got)
	}
}

// const must reuse the shell's own readonly machinery. A const that `declare`
// could quietly reassign would be a lie, and the shell already knows how to
// refuse that.
func TestBashPPConstIsReadOnly(t *testing.T) {
	t.Parallel()
	r := bashPPRunner(t)
	r.bashPPDeclare(context.Background(), &syntax.BashPPDecl{
		Site: syntax.StartConst,
		Kw:   lit("const"),
		Name: lit("K"),
		Init: []*syntax.Word{word("2")},
	})
	vr := r.lookupVar("K")
	if vr.Str != "2" {
		t.Fatalf("K = %q, want %q", vr.Str, "2")
	}
	if !vr.ReadOnly {
		t.Fatal("const K is not read-only; it must reuse the shell's readonly machinery")
	}
}

func TestBashPPShortDeclScalar(t *testing.T) {
	t.Parallel()
	r := bashPPRunner(t)
	r.bashPPShortDecl(context.Background(), &syntax.BashPPShortDecl{
		Lhs:   []*syntax.Lit{lit("x")},
		Rhs:   []*syntax.Word{word("42")},
		Class: syntax.ClassE,
	})
	if got := r.lookupVar("x").Str; got != "42" {
		t.Fatalf("x = %q, want %q", got, "42")
	}
}

func TestBashPPShortDeclTuple(t *testing.T) {
	t.Parallel()
	r := bashPPRunner(t)
	r.bashPPShortDecl(context.Background(), &syntax.BashPPShortDecl{
		Lhs:   []*syntax.Lit{lit("a"), lit("b")},
		Rhs:   []*syntax.Word{word("1"), word("2")},
		Class: syntax.ClassE,
	})
	if got := r.lookupVar("a").Str; got != "1" {
		t.Fatalf("a = %q, want %q", got, "1")
	}
	if got := r.lookupVar("b").Str; got != "2" {
		t.Fatalf("b = %q, want %q", got, "2")
	}
}

// A length mismatch must be a hard error, never a partial bind. Binding what
// fits and leaving the rest at their previous values would leave the shell in
// a state no reader could predict from the source line.
func TestBashPPShortDeclMismatchIsFatal(t *testing.T) {
	t.Parallel()
	var stderr strings.Builder
	r, err := New(Lang(syntax.LangBashPP), StdIO(nil, nil, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	r.Reset()
	r.bashPPShortDecl(context.Background(), &syntax.BashPPShortDecl{
		Lhs:   []*syntax.Lit{lit("a"), lit("b"), lit("c")},
		Rhs:   []*syntax.Word{word("1"), word("2")},
		Class: syntax.ClassE,
	})
	if r.exit.code == 0 {
		t.Fatal("a length mismatch exited 0; it must fail rather than partially bind")
	}
	if !strings.Contains(stderr.String(), "assignment mismatch") {
		t.Fatalf("stderr = %q, want an assignment-mismatch diagnostic", stderr.String())
	}
	if r.lookupVar("a").Str != "" {
		t.Fatal("a was bound despite the mismatch; the bind must be all-or-nothing")
	}
}

// The Class rule, which is the compatibility contract in one function.
//
// A Class R form may be diagnosed, because bash rejects it anyway and nothing
// working is taken away. A Class E form must NEVER be diagnosed, because a
// script relying on today's shell meaning would break. This is the single
// place that rule is implemented, so it is the single place it can regress.
func TestBashPPUnsupportedRespectsClass(t *testing.T) {
	t.Parallel()

	t.Run("ClassE falls back to shell silently", func(t *testing.T) {
		var stderr strings.Builder
		r, err := New(Lang(syntax.LangBashPP), StdIO(nil, nil, &stderr))
		if err != nil {
			t.Fatal(err)
		}
		r.Reset()
		if r.bashPPUnsupported(syntax.ClassE, "switch x { … }") {
			t.Fatal("a Class E form was handled by bash++; it must fall back to shell")
		}
		if stderr.String() != "" {
			t.Fatalf("a Class E form produced a diagnostic (%q); "+
				"that would break a script which runs today", stderr.String())
		}
	})

	t.Run("ClassR may diagnose", func(t *testing.T) {
		var stderr strings.Builder
		r, err := New(Lang(syntax.LangBashPP), StdIO(nil, nil, &stderr))
		if err != nil {
			t.Fatal(err)
		}
		r.Reset()
		if !r.bashPPUnsupported(syntax.ClassR, "go worker(a, b)") {
			t.Fatal("a Class R form fell back to shell; bash rejects it anyway, so a diagnostic is owed")
		}
		if !strings.Contains(stderr.String(), "not supported") {
			t.Fatalf("stderr = %q, want a diagnostic", stderr.String())
		}
	})
}

// Extensions off must be loud, not silent. A Bash++ node reaching a runner with
// objects disabled is a bug in the dispatch, not bad user input, and a silent
// no-op there would be indistinguishable from correct execution.
func TestBashPPDeclareRefusesWithExtensionsOff(t *testing.T) {
	t.Parallel()
	var stderr strings.Builder
	r, err := New(StdIO(nil, nil, &stderr)) // LangBash: no bash++ dialect
	if err != nil {
		t.Fatal(err)
	}
	r.Reset()
	r.bashPPDeclare(context.Background(), &syntax.BashPPDecl{
		Site: syntax.StartVar,
		Kw:   lit("var"),
		Name: lit("x"),
		Init: []*syntax.Word{word("42")},
	})
	if r.exit.code == 0 {
		t.Fatal("evaluating a bash++ node with extensions off exited 0; it must be loud")
	}
	if r.lookupVar("x").Str != "" {
		t.Fatal("x was bound with extensions off")
	}
}
