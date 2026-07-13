// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package syntax

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

// TestBashPPVariant covers the variant's plumbing: it is accepted by the
// parser, and it round-trips through the flag.Value interface.
func TestBashPPVariant(t *testing.T) {
	t.Parallel()

	qt.Assert(t, qt.Equals(LangBashPP.String(), "bashpp"))

	for _, s := range []string{"bashpp", "bash++"} {
		var l LangVariant
		qt.Assert(t, qt.IsNil(l.Set(s)))
		qt.Assert(t, qt.Equals(l, LangBashPP))
	}

	// Adding the variant must not have renumbered the existing bits, as
	// LangVariant values are part of the public API.
	qt.Assert(t, qt.Equals(LangBash, 1<<0))
	qt.Assert(t, qt.Equals(LangPOSIX, 1<<1))
	qt.Assert(t, qt.Equals(LangMirBSDKorn, 1<<2))
	qt.Assert(t, qt.Equals(LangBats, 1<<3))
	qt.Assert(t, qt.Equals(LangZsh, 1<<4))
	qt.Assert(t, qt.Equals(LangAuto, 1<<5))
	qt.Assert(t, qt.Equals(LangBashPP, 1<<6))

	// bash++ is bash-like, and bash-exact: it gets every Bash parse rule,
	// including those which the other bash-like variants do not share.
	qt.Assert(t, qt.IsTrue(LangBashPP.in(langBashLike)))
	qt.Assert(t, qt.IsTrue(LangBashPP.in(langBashExact)))
	qt.Assert(t, qt.IsFalse(LangBats.in(langBashExact)))
}

// TestBashPPMatchesBash is the byte-identical gate. bash++ is a strict superset
// of Bash which, at this level, adds no syntax at all — so for every input in
// the shared corpus, parsing under LangBashPP must produce the same AST and the
// same printed bytes as parsing under LangBash, and must agree on whether the
// input is an error at all.
//
// This is what lets the interpreter gate behaviour on the dialect without any
// risk to the LangBash consumers: at the syntax layer, the two are the same
// language.
func TestBashPPMatchesBash(t *testing.T) {
	t.Parallel()

	// Every input the parser tests know about: the valid corpus, the error
	// corpus, and the printer's own cases.
	var inputs []string
	for _, cs := range [][]fileTestCase{fileTests, fileTestsNoPrint, fileTestsKeepComments} {
		for _, c := range cs {
			inputs = append(inputs, c.inputs...)
		}
	}
	for _, c := range errorCases {
		inputs = append(inputs, c.in)
	}
	for _, c := range printTests {
		inputs = append(inputs, c.in)
	}
	if len(inputs) < 500 {
		t.Fatalf("corpus looks too small to be meaningful: %d inputs", len(inputs))
	}

	parse := func(lang LangVariant, in string) (*File, error) {
		p := NewParser(Variant(lang), KeepComments(true))
		return p.Parse(strings.NewReader(in), "")
	}
	print := func(f *File) (string, error) {
		var sb strings.Builder
		err := NewPrinter().Print(&sb, f)
		return sb.String(), err
	}
	// cmpOpt ignores positions, so compare them separately: identical syntax
	// means every node must start and end at the same offset too.
	positions := func(f *File) []uint {
		var out []uint
		Walk(f, func(n Node) bool {
			if n != nil {
				out = append(out, n.Pos().Offset(), n.End().Offset())
			}
			return true
		})
		return out
	}

	for _, in := range inputs {
		bashFile, bashErr := parse(LangBash, in)
		ppFile, ppErr := parse(LangBashPP, in)

		// Agree on error-ness, and on the error text.
		if (bashErr == nil) != (ppErr == nil) {
			t.Errorf("input %q: bash err=%v but bashpp err=%v", in, bashErr, ppErr)
			continue
		}
		if bashErr != nil {
			// The message embeds the language name, so compare the rest.
			bashMsg := strings.ReplaceAll(bashErr.Error(), "bash", "")
			ppMsg := strings.ReplaceAll(ppErr.Error(), "bashpp", "")
			if bashMsg != ppMsg {
				t.Errorf("input %q: bash err %q != bashpp err %q", in, bashErr, ppErr)
			}
			continue
		}

		// Same AST, down to the node positions.
		qt.Check(t, qt.CmpEquals(ppFile, bashFile, cmpOpt),
			qt.Commentf("AST differs for input %q", in))
		qt.Check(t, qt.DeepEquals(positions(ppFile), positions(bashFile)),
			qt.Commentf("node positions differ for input %q", in))

		// Same printed bytes.
		bashOut, err := print(bashFile)
		qt.Assert(t, qt.IsNil(err))
		ppOut, err := print(ppFile)
		qt.Assert(t, qt.IsNil(err))
		if bashOut != ppOut {
			t.Errorf("input %q: bash printed %q but bashpp printed %q", in, bashOut, ppOut)
		}
	}
}

// TestBashPPQuoteMatchesBash covers the other LangVariant-driven code path in
// this package: Quote's per-language escaping must treat bash++ as bash.
func TestBashPPQuoteMatchesBash(t *testing.T) {
	t.Parallel()

	for _, s := range []string{
		"", "foo", "foo bar", "a'b", `a"b`, "a\\b", "$foo", "a\tb", "a\nb",
		"\x00\x01", "\xff", "héllo", "é", "a`b", "a#b", "a{b,c}",
	} {
		bashOut, bashErr := Quote(s, LangBash)
		ppOut, ppErr := Quote(s, LangBashPP)
		qt.Check(t, qt.Equals(ppOut, bashOut), qt.Commentf("Quote(%q)", s))
		qt.Check(t, qt.Equals(ppErr == nil, bashErr == nil), qt.Commentf("Quote(%q) err", s))
	}
}
