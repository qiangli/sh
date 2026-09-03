// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

// The Bash++ untyped declaration: `var x = 1` and `const K = 2`.
//
// WHERE THE DECISION IS MADE, AND WHY IT IS MADE THERE.
//
// Both keywords are Class E — bash 5.3 runs `var x = 1` today as an ordinary
// command with three arguments — so claiming the shape changes what an
// existing script does, and may only happen when the whole body is one Bash++
// supports. `var x = 1 extra` and `var x = foo bar` are not that body and must
// keep running as the four- and five-word commands they are.
//
// sh's parser is streaming and non-backtracking. It cannot rewind, and it
// cannot promise that more than a handful of bytes past the command position
// are buffered: an earlier attempt to answer a different question by scanning
// ahead for a matching bracket failed three times, always the same way, with
// the scan running off the end of a chunk and the conservative answer silently
// restoring the old behaviour. So the one thing this dispatch must not do is
// consume on the strength of a prefix and hope the rest of the shape arrives.
//
// It therefore consumes NOTHING on speculation. The command is parsed by the
// ordinary shell path into a [CallExpr] — exactly the words LangBash would
// produce, in exactly the same positions — and only then, with the full body
// in hand and the terminator already reached, is it asked whether it is a
// declaration. A body that is not gets handed back unchanged, which is why the
// fallback costs nothing and cannot be observed: there is no state to undo,
// and no point at which a partial commit exists.
//
// The bound is therefore structural rather than a byte budget. The answer
// depends on a fixed number of words at the head of one already-parsed
// command, never on unread input, so it holds identically behind a one-byte
// reader — see TestBashPPUnsupportedDeclBodyStaysShell.
//
// TWO GATES, NOT ONE. "Does a Go region open here" and "is this particular
// form supported" are separate questions, decided in separate places, and
// conflating them is how a Class E fallback turns into a diagnostic:
//
//   - [RecognizeStartSite] owns the first. It is the decision table of record,
//     measured against stock bash, and it answers from the keyword and the
//     name alone. This file does not re-derive that answer; it asks.
//   - This file owns the second. `var x int = 1` opens a real start site and
//     is still not a body P1 implements, so it falls back here — silently,
//     because a Class E shape that Bash++ does not support must keep running
//     as the shell command it already is.

// bashppUntypedDecl reclassifies a completed shell command as a Bash++ untyped
// var/const declaration, or returns nil to leave it exactly as the shell
// parsed it.
//
// redirs is the enclosing statement's redirect list, which the caller has
// finished collecting by the time this runs.
func bashppUntypedDecl(ce *CallExpr, redirs []*Redirect) *BashPPDecl {
	if ce == nil {
		return nil
	}
	// A declaration has nowhere to put a prefix assignment or a redirect, and
	// bash gives both a meaning here (`e=1 var …`, `var … > out`). Leaving
	// them shell is the only answer that keeps that meaning.
	if len(ce.Assigns) > 0 || len(redirs) > 0 {
		return nil
	}

	// The supported body, in full: keyword, name, `=`, one initializer word.
	// Any other arity is a different shape — `var x int = 1` is the typed form
	// this story does not implement, `var x = 1 extra` is a four-argument
	// command — and a different shape is somebody else's, so it stays shell.
	if len(ce.Args) != 4 {
		return nil
	}
	kw := bashppBareLit(ce.Args[0])
	name := bashppBareLit(ce.Args[1])
	eq := bashppBareLit(ce.Args[2])
	if kw == nil || name == nil || eq == nil {
		return nil
	}
	if eq.Value != "=" || !bashppIsIdent(name.Value) {
		return nil
	}
	// The initializer must be one the Day-1 grammar actually spells out. An
	// arity check alone claimed `var x = 1,` and `var x = {1}` — four-word
	// commands whose fourth word is not a Go expression at all — which is the
	// same Class E mistake as committing on a prefix, made one word later.
	if bashppInitKind(ce.Args[3]) == "" {
		return nil
	}

	// Gate one: the decision table decides whether a Go region opens at all.
	// Asking it rather than re-testing the keyword here is what keeps a single
	// source of truth for the start sites; the corpus measures that table, and
	// a second copy of the rule would be free to drift away from what was
	// measured. `type T = string` reaches this point and is refused, because
	// StartTypeDecl is not a site this story implements.
	m := RecognizeStartSite(kw.Value + " " + name.Value)
	switch m.Site {
	case StartVar, StartConst:
	default:
		return nil
	}

	return &BashPPDecl{
		Site: m.Site,
		Kw:   kw,
		Name: name,
		// DeclType stays nil: this is the untyped form, and the typed one is
		// a separate body that falls back above.
		Init: ce.Args[3:4:4],
		End_: ce.Args[3].End(),
	}
}

// bashppBareLit returns the word's sole literal part, or nil when the word is
// anything else.
//
// "Anything else" is doing real work. It is what makes the published escapes
// win without a special case: `"var" x = 1` and `'var' x = 1` have a quoted
// part rather than a bare one, and `var $x = 1` has a parameter expansion, so
// none of them is a bare literal and none of them opens a declaration. The
// single-part requirement matters for the same reason — `var x"y" = 1` joins
// two parts into one word, and a word that needs quoting to spell its own name
// is not a Go identifier.
func bashppBareLit(w *Word) *Lit {
	if w == nil || len(w.Parts) != 1 {
		return nil
	}
	lit, _ := w.Parts[0].(*Lit)
	return lit
}

// bashppIsIdent reports whether s is a complete Go identifier.
//
// [leadingIdent] is deliberately not reused: it returns the identifier PREFIX
// of its input, so it accepts `x` out of `x-y`, which would let `var x-y = 1`
// — a perfectly ordinary bash command — be claimed on the strength of its
// first byte.
//
// Go keywords are excluded, because Go excludes them: `if`, `type` and
// `return` are not identifiers, so `var if = 1` is not an unsupported
// declaration, it is not a declaration at all. Bash runs all three today as
// ordinary three-argument commands, so claiming them would change what a
// working script does at a site no phase can ever legitimately claim. The
// check is duplicated in [RecognizeStartSite] rather than only here on
// purpose: the two gates answer different questions, and a shape that can
// never be a Go region should not reach the second gate as a Class E hit.
func bashppIsIdent(s string) bool {
	if s == "" || isGoReservedWord(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i], i == 0) {
			return false
		}
	}
	return true
}

// THE DAY-1 INITIALIZER GRAMMAR, in full:
//
//	Init  ::= IntLit
//	IntLit ::= "0" | ("1"…"9") {"0"…"9"}
//
// That is the whole of it, and the narrowness is the point rather than an
// omission to be tidied up later.
//
// WHY A GRAMMAR AND NOT A WORD COUNT. The first cut of this dispatch checked
// only that the command had four words, which meant the fourth word could be
// anything the shell can spell. `var x = 1,` and `var x = {1}` are ordinary
// bash commands whose last argument is not a Go expression in any reading, and
// both were claimed. A Class E site may only be claimed for a body Bash++
// actually supports, and "some word" is not a body; the grammar is what makes
// "supported" a decidable question instead of an arity coincidence.
//
// WHY ONLY INTEGERS. The published Class E rows for the untyped form are
// `var x = 1` and `const K = 2` — decimal integers, and nothing else. A
// divergence is licensed by the row that names its shape, so claiming a string
// or a float initializer would be claiming a shape no row describes and no
// corpus run measured. The remedy for widening it is to measure the shape into
// bashpp-tests/tools/startsites and publish the row, not to loosen this
// function; TestBashPPAcceptedShapesAreLicensed fails if the two drift apart.
//
// Deliberately absent, each falling back to the shell in silence:
//
//   - string, rune and float literals — `var x = "a b"`, `var x = 'q'`,
//     `var x = 1.5`. Unmeasured, hence unlicensed; the `:=` site has a
//     published string row but the `var` site does not, and a row licenses its
//     own shape.
//   - non-decimal and separated integers — `0x1f`, `0b1`, `007`, `1_000`.
//     `007` is the sharpest of these: Go reads it as octal and the shell reads
//     it as the characters `007`, so the two languages disagree about the
//     value of a shape that is Class E in bash today.
//   - signed values — `-1`, `+1`. A leading `-` is also how a bash command
//     spells a flag, so `var x = -f` and `var x = -1` are the same shape to
//     the shell.
//   - anything with an expansion, a quote or a glob in it, which
//     [bashppBareLit] already refuses one layer up.

// bashppInitKind names the Day-1 initializer form w takes, or returns the
// empty string when w is not an initializer this phase supports.
//
// The kind, rather than the value, is what the compatibility gate matches a
// published row against: `var x = 1` and `var counter = 42` are one shape and
// one row, while `var x = "a b"` is a different shape and would need its own.
func bashppInitKind(w *Word) string {
	lit := bashppBareLit(w)
	if lit == nil {
		return ""
	}
	if !bashppIsDecimalInt(lit.Value) {
		return ""
	}
	return "INT"
}

// bashppIsDecimalInt reports whether s is a Go decimal_lit with no underscores.
//
// Leading zeros are refused rather than tolerated: `007` is octal in Go and the
// literal characters `007` in the shell, so a shape the two languages read
// differently is exactly the shape a Class E site must not claim.
func bashppIsDecimalInt(s string) bool {
	if s == "" {
		return false
	}
	if s == "0" {
		return true
	}
	if s[0] < '1' || s[0] > '9' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
