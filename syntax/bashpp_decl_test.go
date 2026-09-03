// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"io"
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
	{"unsupported after supported", "var x = 1\nvar y = 2 extra"},

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
