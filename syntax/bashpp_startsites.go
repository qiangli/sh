// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import "strings"

// This file is the Bash++ start-site DECISION TABLE, extracted from the parser
// rather than embedded in it.
//
// What it is: the predicate the command-position dispatch will consult to
// decide whether a Go region opens here. Keeping it separate is not tidiness.
// It makes the decision unit-testable against the measured corpus in
// bashpp-tests/tools/startsites WITHOUT a parser, and it reduces the eventual
// edit to certification-owned parser.go to a single call.
//
// What it is NOT: the wired dispatch. Nothing calls RecognizeStartSite yet.
// Until parser.go is edited, Bash++ recognizes nothing and LangBashPP remains
// byte-identical to LangBash, exactly as its doc comment promises.
//
// THE BOUNDED-LOOKAHEAD PROPERTY, and why it is load-bearing.
//
// sh's parser is streaming and non-backtracking: it reads the input in chunks
// and cannot rewind. An earlier attempt to fix an unrelated defect by scanning
// ahead for a matching bracket failed three times for exactly this reason —
// the scan ran off the end of the buffered chunk, and the conservative answer
// at a chunk boundary silently restored the old behaviour, which is the worst
// possible failure because it looks like success.
//
// So every recognizer below is required to decide within maxLookahead bytes of
// the command position, and a test asserts it. A site that cannot is not
// "harder to implement" — it is UNIMPLEMENTABLE in this parser without a
// different mechanism, and must be identified now rather than discovered at
// the third failed attempt.
//
// StartGoIf is the known exception and is deliberately excluded from the
// bounded set: a shell `if` may legally carry `{` as the last word of its
// condition and continue with `then`, so only the absence of `then` after the
// MATCHING brace commits. That is unbounded by construction. It is recorded
// here as owed design work, not scheduled as ordinary implementation.

// maxLookahead is the byte budget a start-site recognizer may examine past the
// command position. It is deliberately small: the whole point is that the
// decision fits comfortably inside one buffered chunk.
const maxLookahead = 64

// StartSiteMatch is the verdict for one command position.
type StartSiteMatch struct {
	Site  StartSite
	Class SiteClass

	// Bounded reports whether the verdict was reached inside maxLookahead.
	// A false here on a supposedly-bounded site is a bug in the table, not a
	// property of the input, and the test suite treats it as one.
	Bounded bool
}

// noMatch is the fail-safe verdict: stay shell. Every early return uses it, so
// that a recognizer which falls through cannot accidentally claim a shape.
var noMatch = StartSiteMatch{Site: StartNone}

// RecognizeStartSite decides whether a Bash++ region opens at the start of src,
// which the caller supplies as the source text from the command position.
//
// It answers only for the P1 (Day-1) sites. Every later phase's shape returns
// noMatch, which is the correct answer for an unimplemented phase: a Class E
// shape must keep running as the shell command it is today, and a Class R
// shape is a bash syntax error either way.
//
// The caller must not treat a match as permission to parse: the shell escapes
// (`command var …`, `"var" …`) are handled BEFORE this is consulted, because
// they work by making the position not a Bash++ command position at all.
func RecognizeStartSite(src string) StartSiteMatch {
	if len(src) > maxLookahead {
		src = src[:maxLookahead]
	}
	s := strings.TrimLeft(src, " \t")
	if s == "" {
		return noMatch
	}

	if m := recognizeKeywordDecl(s); m.Site != StartNone {
		return m
	}
	if recognizeImportPrefix(s) {
		return StartSiteMatch{Site: StartImport, Class: ClassE, Bounded: true}
	}
	if m := recognizeShortDecl(s); m.Site != StartNone {
		return m
	}
	if m := recognizeGoCall(s); m.Site != StartNone {
		return m
	}
	return noMatch
}

func recognizeImportPrefix(s string) bool {
	rest, ok := cutKeyword(s, "import")
	if !ok {
		return false
	}
	if strings.HasPrefix(rest, `"`) {
		return true
	}
	if strings.HasPrefix(rest, "_") || strings.HasPrefix(rest, ".") {
		rest = strings.TrimLeft(rest[1:], " \t")
		return strings.HasPrefix(rest, `"`)
	}
	alias := leadingIdent(rest)
	if alias == "" || isGoReservedWord(alias) {
		return false
	}
	rest = strings.TrimLeft(rest[len(alias):], " \t")
	return strings.HasPrefix(rest, `"`)
}

// recognizeKeywordDecl handles var, const and type.
//
// All three are Class E — `var`, `const` and `type` are all ordinary command
// words in bash today (`type` is even a builtin), so each carries a published
// table row and a `command …` escape. The signal is the keyword FOLLOWED BY a
// blank and an identifier: `var x` commits, bare `var` does not, because a
// script calling a program named `var` with no arguments must keep working.
func recognizeKeywordDecl(s string) StartSiteMatch {
	for kw, site := range map[string]StartSite{
		"var":   StartVar,
		"const": StartConst,
		"type":  StartTypeDecl,
	} {
		rest, ok := cutKeyword(s, kw)
		if !ok {
			continue
		}
		// An identifier must follow. Without one this is a plain command
		// invocation and must stay shell. A Go keyword is not an identifier,
		// so `var if = 1` and `const return = 1` are refused here rather than
		// downstream: no phase of Bash++ can ever declare a name Go reserves,
		// while bash runs both today as ordinary commands.
		name := leadingIdent(rest)
		if name == "" || isGoReservedWord(name) {
			continue
		}
		return StartSiteMatch{Site: site, Class: ClassE, Bounded: true}
	}
	return noMatch
}

// recognizeShortDecl handles the := sites, which SPLIT across both classes.
//
// This split is the single most useful thing the measured corpus produced, and
// it is invisible to inspection: `x := 42` is Class E (bash runs a command `x`
// with arguments `:=` and `42`), while `x := f()` is Class R (the parenthesis
// after a word is already a syntax error). One start site, two rows, opposite
// compatibility risk. The class is recorded on the node so no later phase has
// to re-derive it.
func recognizeShortDecl(s string) StartSiteMatch {
	rest, ok := cutShortDeclLhs(s)
	if !ok {
		return noMatch
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		// `x :=` with nothing after it. Bash runs command `x` with argument
		// `:=`, so the fail-safe answer is shell.
		return noMatch
	}
	// A call on the right-hand side makes the whole shape a bash syntax error,
	// hence Class R. Anything else (a scalar, a composite literal, a tuple) is
	// a shape bash accepts today, hence Class E.
	class := ClassE
	if hasCallShape(rest) {
		class = ClassR
	}
	return StartSiteMatch{Site: StartShortDecl, Class: class, Bounded: true}
}

// recognizeGoCall handles a bare call in command position: f(1, 2), x.y.z().
//
// Always Class R. Bash's only `word (` production is the function definition
// `name ()`, so a call with arguments, or with a dotted selector, is already a
// parse error. An empty argument list is excluded precisely BECAUSE `f ()` is
// the function-definition form and is legal shell.
func recognizeGoCall(s string) StartSiteMatch {
	ident := leadingSelector(s)
	if ident == "" {
		return noMatch
	}
	// A callee is an identifier too, so a Go keyword cannot head one: `if(x)`
	// is not a Go call and must not be claimed as one.
	head, _, _ := strings.Cut(ident, ".")
	if isGoReservedWord(head) {
		return noMatch
	}
	rest := s[len(ident):]
	if !strings.HasPrefix(rest, "(") {
		return noMatch
	}
	// `f()` with a dot is a Go selector call and cannot be a bash function
	// definition, so it commits. `f()` without one is exactly the shape of a
	// bash function definition and must stay shell.
	if strings.HasPrefix(rest, "()") && !strings.Contains(ident, ".") {
		return noMatch
	}
	return StartSiteMatch{Site: StartGoCall, Class: ClassR, Bounded: true}
}

// cutKeyword returns the remainder after kw, but only when kw stands as a
// complete word. `variable=1` must not match the `var` keyword.
func cutKeyword(s, kw string) (string, bool) {
	if !strings.HasPrefix(s, kw) {
		return "", false
	}
	rest := s[len(kw):]
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	return strings.TrimLeft(rest, " \t"), true
}

// cutShortDeclLhs matches `name :=` or `a, b :=` and returns the remainder.
func cutShortDeclLhs(s string) (string, bool) {
	rest := s
	for {
		name := leadingIdent(rest)
		// Same rule as the keyword declarations: a Go keyword cannot be a
		// declaration target, so `if := 1` is a shell command and nothing else.
		if name == "" || isGoReservedWord(name) {
			return "", false
		}
		rest = strings.TrimLeft(rest[len(name):], " \t")
		if strings.HasPrefix(rest, ",") {
			rest = strings.TrimLeft(rest[1:], " \t")
			continue
		}
		if strings.HasPrefix(rest, ":=") {
			return rest[2:], true
		}
		return "", false
	}
}

// hasCallShape reports whether the right-hand side opens with a call, which is
// what makes a := shape Class R rather than Class E.
func hasCallShape(s string) bool {
	ident := leadingSelector(s)
	if ident == "" {
		return false
	}
	return strings.HasPrefix(s[len(ident):], "(")
}

func leadingIdent(s string) string {
	i := 0
	for i < len(s) && isIdentByte(s[i], i == 0) {
		i++
	}
	return s[:i]
}

// leadingSelector matches an identifier possibly followed by .field chains.
func leadingSelector(s string) string {
	i := 0
	for {
		part := leadingIdent(s[i:])
		if part == "" {
			return ""
		}
		i += len(part)
		if i < len(s) && s[i] == '.' {
			i++
			continue
		}
		return s[:i]
	}
}

func isIdentByte(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return !first
	}
	return false
}

// goReservedWords is the complete set of Go keywords, as of the Go 1.x
// specification's "Keywords" section. All 25 are listed rather than only the
// ones a Day-1 shape could plausibly collide with, because the closed set is
// the specification's and a hand-picked subset would be a second, drifting
// answer to a question Go has already answered.
//
// WHY THE RECOGNIZER CARES. A Go keyword is not an identifier, so `var if = 1`
// is not a declaration in any phase of Bash++ — it is not a Go region that
// happens to be unsupported, it is not a Go region at all. Bash, meanwhile,
// runs it today as a three-argument command. Letting the recognizer fire there
// would put a shape that can never be claimed onto the Class E ledger, and the
// dispatch would have to refuse it a second time to avoid changing what a
// working script does. One refusal, at the point where the name is required to
// be an identifier, is the whole of it.
var goReservedWords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// isGoReservedWord reports whether s is a Go keyword and therefore may never
// be used as a declared name, a short-declaration target or a callee.
func isGoReservedWord(s string) bool { return goReservedWords[s] }
