// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

// THE UNSUPPORTED-BODY REGRESSION, and why it is written before the parser
// dispatch that makes it interesting.
//
// The Day-1 `var`/`const` sites are Class E: `var x = 1` runs today as an
// ordinary command with three arguments. So the dispatch may only claim a
// command once it knows the WHOLE body is a form Bash++ supports. The failure
// this file exists to catch is the parser committing on the PREFIX — seeing
// `var` and an identifier, deciding a Go region has opened, and then finding
// `extra` or `bar` where it wanted a terminator. At that point the tokens are
// spent: a streaming, non-backtracking parser cannot put them back, so the
// only exits are a diagnostic (which breaks a working script — the one
// outcome the design forbids for Class E) or a half-built node.
//
// `var x = 1 extra` and `var x = foo bar` are the two cheapest witnesses. Both
// open exactly like the supported form and neither IS it, so a recognizer that
// answers from the prefix cannot tell them apart from `var x = 1`, while one
// that waits for the full body separates them without effort.
//
// The four configurations are not decoration:
//
//   - PosixMode off/on, because [Parser.posixBehavior] changes parse rules
//     underneath the dispatch, and a fallback that only survives one of them
//     is not a fallback.
//   - a whole-string reader and a ONE-BYTE reader, because the parser is
//     streaming over an [io.Reader] and its buffer boundary is where
//     lookahead-based designs have historically failed — the conservative
//     answer at a chunk boundary silently restores the old behaviour, which
//     looks exactly like success. A one-byte reader puts a chunk boundary
//     between every pair of bytes, so any hidden dependence on "the rest of
//     the shape happens to be buffered" shows up as a diff here.
//
// The assertion is identity with LangBash — same AST, same node positions,
// same printed bytes — which is the only claim that cannot be satisfied by a
// node that merely happens to run the same way.

// bashppReadModes feeds the same source to the parser two ways: in one piece,
// and one byte at a time.
var bashppReadModes = []struct {
	name string
	wrap func(string) io.Reader
}{
	{"whole", func(s string) io.Reader { return strings.NewReader(s) }},
	{"one-byte", func(s string) io.Reader { return iotest.OneByteReader(strings.NewReader(s)) }},
}

// bashppParseAs parses in under lang in one specific configuration. Comments
// are kept so that a divergence in comment placement cannot hide.
func bashppParseAs(lang LangVariant, in string, posix bool, wrap func(string) io.Reader) (*File, error) {
	p := NewParser(Variant(lang), KeepComments(true), PosixMode(posix))
	return p.Parse(wrap(in), "")
}

// bashppCheckIdentical asserts that LangBashPP is indistinguishable from
// LangBash for in, in every configuration of bashppReadModes x PosixMode.
func bashppCheckIdentical(t *testing.T, in string) {
	t.Helper()
	for _, posix := range []bool{false, true} {
		for _, mode := range bashppReadModes {
			name := mode.name
			if posix {
				name += "/posix"
			}
			t.Run(name, func(t *testing.T) {
				bashFile, bashErr := bashppParseAs(LangBash, in, posix, mode.wrap)
				ppFile, ppErr := bashppParseAs(LangBashPP, in, posix, mode.wrap)
				switch {
				case (bashErr == nil) != (ppErr == nil):
					t.Fatalf("input %q: bash err=%s but bashpp err=%s",
						in, errText(bashErr), errText(ppErr))
				case bashErr != nil:
					if diff := bashppErrDiff(bashErr, ppErr); diff != "" {
						t.Fatalf("input %q: %s", in, diff)
					}
				default:
					if diff := bashppTreeDiff(bashFile, ppFile); diff != "" {
						t.Fatalf("input %q must stay shell, but bashpp parsed it "+
							"differently: %s", in, diff)
					}
				}
			})
		}
	}
}

// bashppUnsupportedDeclBodies are the shapes that open like a Day-1 var/const
// declaration and are not one. Every entry must parse, print and position
// EXACTLY as LangBash does; claiming any of them would change what a working
// script does at a site bash accepts today.
var bashppUnsupportedDeclBodies = []struct{ name, in string }{
	// The two named witnesses: a supported body with a word glued on the end.
	// A prefix-committing dispatch claims both.
	{"trailing word after value", "var x = 1 extra"},
	{"two words after =", "var x = foo bar"},
	{"const with trailing word", "const K = 2 extra"},
	{"many trailing words", "var x = 1 a b c d e"},

	// Arity below the supported form.
	{"bare keyword", "var"},
	{"keyword and name only", "var x"},
	{"no value", "var x ="},
	{"const bare", "const"},

	// TYPED forms. This story implements the UNTYPED declaration only, so a
	// type annotation is an unsupported body and must fall back — not be
	// half-claimed, and not diagnosed.
	{"typed with value", "var x int = 1"},
	{"typed without value", "var x int"},
	{"const typed", "const K int = 2"},

	// The separator is not `=`.
	{"short decl operator", "var x := 1"},
	{"no separator", "var x 1"},
	{"glued assignment", "var x=1"},
	{"keyword then bare =", "var = 1"},

	// The name is not a Go identifier, or is not a bare word at all.
	{"hyphenated name", "var x-y = 1"},
	{"expanded name", "var $x = 1"},
	{"quoted name", `var "x" = 1`},
	{"flag-shaped name", "var -x = 1"},

	// The published escapes. These work by making the position not a Bash++
	// command position at all, so they must be identical whatever the body.
	{"command escape", "command var x = 1"},
	{"quoted keyword escape", `"var" x = 1`},
	{"single-quoted keyword escape", `'var' x = 1`},

	// Redirects mean the shell is doing something the declaration shape has
	// nowhere to put, so the whole command stays shell.
	{"trailing redirect", "var x = 1 > out"},
	{"leading redirect", "> out var x = 1"},

	// A leading assignment is a shell prefix assignment, not a declaration.
	{"assignment prefix", "e=1 var x = 1"},

	// Multi-line inputs, where the unsupported body is not the first command
	// and the parser has already been through a newline.
	{"unsupported on second line", "echo hi\nvar x = 1 extra"},
	{"two unsupported lines", "var x = 1 extra\nvar y = 2 extra"},

	// Nested command positions: the dispatch fires at every command position,
	// so the fallback has to hold at every one of them too.
	{"inside command substitution", "echo $(var x = 1 extra)"},
	{"inside a function body", "f() { var x = 1 extra; }"},
	{"inside an if", "if true; then var x = foo bar; fi"},
}

// TestBashPPUnsupportedDeclBodyStaysShell is the regression named above: an
// unsupported declaration body must leave LangBashPP byte-identical to
// LangBash, in every reader and POSIX configuration.
func TestBashPPUnsupportedDeclBodyStaysShell(t *testing.T) {
	t.Parallel()

	for _, tc := range bashppUnsupportedDeclBodies {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bashppCheckIdentical(t, tc.in)
		})
	}
}

// THE ACCEPTED SIDE. Everything above proves the dispatch keeps its hands off
// what it does not understand; what follows proves it does something exact
// with what it does.
//
// The claim under test is not "a BashPPDecl appears". It is that the node
// carries the WHOLE classification — which start site opened it, which bytes
// each part came from — because the design of record makes this the only place
// the decision is stored. No later phase re-reads the source to work out
// whether a region is Go or shell; the node's existence is the decision, and
// its positions are the only record of where the decision applies. A node with
// a right shape and wrong positions would print correctly today and misplace
// every diagnostic and every edit an embedder makes tomorrow.

// bashppAcceptedDecl is one claimed declaration, spelled out down to the byte
// offsets its parts must occupy.
type bashppAcceptedDecl struct {
	in   string
	site StartSite
	kw   string
	name string
	init string

	// Byte offsets, half-open, of the whole declaration and of each part.
	// Written out rather than derived from in, so that a change in what the
	// dispatch consumes has to be restated here deliberately.
	declStart, declEnd uint
	kwStart, kwEnd     uint
	nameStart, nameEnd uint
	initStart, initEnd uint
}

var bashppAcceptedDecls = []bashppAcceptedDecl{
	{
		in: "var x = 1", site: StartVar, kw: "var", name: "x", init: "1",
		declStart: 0, declEnd: 9,
		kwStart: 0, kwEnd: 3,
		nameStart: 4, nameEnd: 5,
		initStart: 8, initEnd: 9,
	},
	{
		in: "const K = 2", site: StartConst, kw: "const", name: "K", init: "2",
		declStart: 0, declEnd: 11,
		kwStart: 0, kwEnd: 5,
		nameStart: 6, nameEnd: 7,
		initStart: 10, initEnd: 11,
	},
	{
		// Irregular spacing: the declaration ENDS at the value, not at the
		// trailing blank, and each part still points at its own bytes. This is
		// the case that catches a node built from token counts instead of from
		// the words the parser produced.
		in: "var   longName   =   value  ", site: StartVar, kw: "var", name: "longName", init: "value",
		declStart: 0, declEnd: 26,
		kwStart: 0, kwEnd: 3,
		nameStart: 6, nameEnd: 14,
		initStart: 21, initEnd: 26,
	},
	{
		// Not at the start of the input, so the offsets cannot come out right
		// by accident.
		in: "echo hi\nvar x = 1", site: StartVar, kw: "var", name: "x", init: "1",
		declStart: 8, declEnd: 17,
		kwStart: 8, kwEnd: 11,
		nameStart: 12, nameEnd: 13,
		initStart: 16, initEnd: 17,
	},
	{
		// An initializer that is not a bare literal. The word is carried
		// UNEVALUATED, exactly as the parser built it, because P1 classifies
		// and does not interpret — the evaluator is a separate story.
		in: `var x = "a b"`, site: StartVar, kw: "var", name: "x", init: "",
		declStart: 0, declEnd: 13,
		kwStart: 0, kwEnd: 3,
		nameStart: 4, nameEnd: 5,
		initStart: 8, initEnd: 13,
	},
}

// bashppLastDecl finds the single *BashPPDecl in f, failing if there is not
// exactly one.
func bashppLastDecl(t *testing.T, f *File) *BashPPDecl {
	t.Helper()
	var found []*BashPPDecl
	Walk(f, func(n Node) bool {
		if d, ok := n.(*BashPPDecl); ok {
			found = append(found, d)
		}
		return true
	})
	if len(found) != 1 {
		t.Fatalf("found %d BashPPDecl nodes, want exactly 1", len(found))
	}
	return found[0]
}

// TestBashPPUntypedDeclClassified is the positive counterpart to
// TestBashPPUnsupportedDeclBodyStaysShell: the supported body is claimed, in
// every reader and POSIX configuration, and the node records where it came
// from.
func TestBashPPUntypedDeclClassified(t *testing.T) {
	t.Parallel()

	for _, tc := range bashppAcceptedDecls {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			for _, posix := range []bool{false, true} {
				for _, mode := range bashppReadModes {
					name := mode.name
					if posix {
						name += "/posix"
					}
					t.Run(name, func(t *testing.T) {
						f, err := bashppParseAs(LangBashPP, tc.in, posix, mode.wrap)
						if err != nil {
							t.Fatalf("LangBashPP rejected %q: %v", tc.in, err)
						}
						decl := bashppLastDecl(t, f)

						if decl.Site != tc.site {
							t.Errorf("site = %v, want %v", decl.Site, tc.site)
						}
						if decl.Kw.Value != tc.kw {
							t.Errorf("keyword = %q, want %q", decl.Kw.Value, tc.kw)
						}
						if decl.Name.Value != tc.name {
							t.Errorf("name = %q, want %q", decl.Name.Value, tc.name)
						}
						// The UNTYPED form is the whole of this story, so a
						// claimed node may never carry a type. The typed body
						// falls back to shell instead; see
						// bashppUnsupportedDeclBodies.
						if decl.DeclType != nil {
							t.Errorf("type = %v, want nil: only the untyped form is claimed", decl.DeclType)
						}
						if len(decl.Init) != 1 {
							t.Fatalf("initializer has %d words, want 1", len(decl.Init))
						}
						if got := decl.Init[0].Lit(); got != tc.init {
							t.Errorf("initializer literal = %q, want %q", got, tc.init)
						}

						checkSpan := func(what string, n Node, start, end uint) {
							t.Helper()
							if got, want := n.Pos().Offset(), start; got != want {
								t.Errorf("%s starts at offset %d, want %d", what, got, want)
							}
							if got, want := n.End().Offset(), end; got != want {
								t.Errorf("%s ends at offset %d, want %d", what, got, want)
							}
						}
						checkSpan("declaration", decl, tc.declStart, tc.declEnd)
						checkSpan("keyword", decl.Kw, tc.kwStart, tc.kwEnd)
						checkSpan("name", decl.Name, tc.nameStart, tc.nameEnd)
						checkSpan("initializer", decl.Init[0], tc.initStart, tc.initEnd)
					})
				}
			}
		})
	}
}

// TestBashPPDeclWalk pins the visitor traversal.
//
// Walk is not a formality for a new Command: its default arm panics on a node
// type it does not know, so an unlisted node turns every Walk-based consumer —
// shfmt -s, every embedder's own analysis, and the compatibility gate's own
// position comparison in bashppNodeOffsets — into a crash. The order and the
// spans are asserted together because a traversal that visits the right nodes
// in the wrong order is just as wrong for a printer driven by positions.
func TestBashPPDeclWalk(t *testing.T) {
	t.Parallel()

	f, err := bashppParse(LangBashPP, "var x = 1")
	if err != nil {
		t.Fatal(err)
	}

	var visited []string
	Walk(f, func(n Node) bool {
		if n == nil {
			return true
		}
		visited = append(visited, fmt.Sprintf("%T[%d,%d)",
			n, n.Pos().Offset(), n.End().Offset()))
		return true
	})

	want := []string{
		"*syntax.File[0,9)",
		"*syntax.Stmt[0,9)",
		"*syntax.BashPPDecl[0,9)",
		"*syntax.Lit[0,3)",  // var
		"*syntax.Lit[4,5)",  // x
		"*syntax.Word[8,9)", // the initializer
		"*syntax.Lit[8,9)",
	}
	if !slices.Equal(visited, want) {
		t.Fatalf("Walk visited\n  %v\nwant\n  %v", visited, want)
	}

	// The `=` at offset 6 is deliberately absent from the tree. BashPPDecl has
	// no field for it — the separator is part of the shape, not part of the
	// declaration's content — so the printer writes it back from the shape
	// rather than from a recorded position. Asserted so that adding a field
	// for it later is a decision rather than an accident.
	for _, v := range visited {
		if strings.Contains(v, "[6,7)") {
			t.Errorf("Walk visited a node at the `=` (offset 6): %s", v)
		}
	}
}

// TestBashPPDeclPrint covers the printer arm, which is the other half of
// "include Walk/Printer positions".
//
// Idempotence is the real assertion. A claimed declaration must print to
// something LangBashPP re-parses into the same declaration, or the tree stops
// round-tripping through shfmt and the classification is lost on the first
// format. Printing the same bytes as LangBash is not required — this is a
// licensed divergence — but it is what these shapes happen to do, and that is
// worth pinning: it means formatting a script does not rewrite the lines
// Bash++ claims.
func TestBashPPDeclPrint(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"var x = 1", "var x = 1\n"},
		{"const K = 2", "const K = 2\n"},
		{"var   longName   =   value  ", "var longName = value\n"},
		{`var x = "a b"`, "var x = \"a b\"\n"},
		{"var x = 1; echo done", "var x = 1\necho done\n"},
		{"var x = 1 | cat", "var x = 1 | cat\n"},
		{"if true; then var x = 1; fi", "if true; then var x = 1; fi\n"},
		{"echo $(var x = 1)", "echo $(var x = 1)\n"},
		{"f() { const K = 2; }", "f() { const K = 2; }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			f, err := bashppParse(LangBashPP, tc.in)
			if err != nil {
				t.Fatalf("LangBashPP rejected %q: %v", tc.in, err)
			}
			got, err := bashppPrint(f)
			if err != nil {
				t.Fatalf("printing %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("printed %q, want %q", got, tc.want)
			}

			// Round-trip: the printed form must re-parse to the same tree,
			// and printing it again must be a fixed point.
			f2, err := bashppParse(LangBashPP, got)
			if err != nil {
				t.Fatalf("LangBashPP rejected its own output %q: %v", got, err)
			}
			if _, ok := bashppFirstDecl(f2); !ok {
				t.Fatalf("re-parsing %q lost the declaration", got)
			}
			got2, err := bashppPrint(f2)
			if err != nil {
				t.Fatalf("re-printing %q: %v", got, err)
			}
			if got2 != got {
				t.Fatalf("printing is not idempotent: %q then %q", got, got2)
			}
		})
	}
}

func bashppFirstDecl(f *File) (*BashPPDecl, bool) {
	var found *BashPPDecl
	Walk(f, func(n Node) bool {
		if found != nil {
			return false
		}
		if d, ok := n.(*BashPPDecl); ok {
			found = d
		}
		return true
	})
	return found, found != nil
}

// TestBashPPDeclOnlyInBashPP is the containment check. Every other variant —
// including the other bash-like ones, which share almost all of the grammar —
// must be untouched by the dispatch, or this stops being an opt-in dialect.
func TestBashPPDeclOnlyInBashPP(t *testing.T) {
	t.Parallel()

	for _, lang := range []LangVariant{LangBash, LangPOSIX, LangMirBSDKorn, LangBats, LangZsh} {
		t.Run(lang.String(), func(t *testing.T) {
			f, err := bashppParse(lang, "var x = 1")
			if err != nil {
				t.Fatalf("%v rejected %q: %v", lang, "var x = 1", err)
			}
			if d, ok := bashppFirstDecl(f); ok {
				t.Fatalf("%v produced a %T; the dispatch must be LangBashPP-only", lang, d)
			}
		})
	}
}
