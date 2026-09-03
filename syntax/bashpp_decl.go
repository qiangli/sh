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
func bashppIsIdent(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i], i == 0) {
			return false
		}
	}
	return true
}
