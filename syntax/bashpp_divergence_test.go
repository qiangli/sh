// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package syntax

import (
	"fmt"
	"strings"
	"testing"
)

// THE PUBLISHED DIVERGENCE ALLOWLIST — the executable form of the Bash++
// compatibility contract.
//
// WHY THIS FILE EXISTS. TestBashPPMatchesBash used to demand byte-identical
// ASTs between LangBash and LangBashPP across the whole parser corpus. That
// assertion was true only while the command-position dispatch was absent, and
// the dispatch breaks it BY DESIGN — at the published Class E sites and nowhere
// else. Weakening or deleting the test would throw away the one gate that can
// tell a designed divergence from a regression, so instead the claim is
// re-expressed: identical everywhere EXCEPT at published Class E rows, each
// divergence named and traceable to a table row, and a divergence not in the
// table is a FAILURE.
//
// THE THREE RULES the re-expressed gate enforces, in TestBashPPMatchesBash:
//
//  1. NEVER LOSE. If LangBash parses an input, LangBashPP must parse it too.
//     Bash++ is a superset; it may never reject a script bash accepts,
//     whatever the class.
//  2. MEANING PRESERVED, OR NAMED. If LangBash parses an input, the two ASTs,
//     node positions and printed bytes must be identical UNLESS a published
//     Class E row licenses the divergence at a real bash command position.
//     An unlicensed difference fails.
//  3. ADDITIVE ONLY. If LangBash rejects an input, LangBashPP may accept it —
//     that is Class R ground, which by definition no working script occupies —
//     but the acceptance must still be attributable to a recognized start
//     site, so that a parser bug cannot hide as "purely additive".
//
// WHY CLASS E IS THE ONLY LICENSE. The class is the measured answer to "what
// does stock bash 5.3 do with this shape". Class R means bash REJECTS it, so
// claiming the shape takes nothing away from anyone and needs no row. Class E
// means bash ACCEPTS it, so claiming it changes what an existing script does —
// which is exactly the thing that has to be published, escapable and, here,
// enumerated.
//
// TRACEABILITY, AND WHAT THIS TABLE MAY NOT DO. The design of record is
// explicit that a Go test may NOT assert a shape's class: that is the corpus's
// job, and a second source of truth can drift. So no row below measures
// anything. Each row CITES a corpusID, and the chain is:
//
//	bashppAllowedDivergences  (this file: which rows license divergence)
//	  -> day1Cases            (bashpp_startsites_test.go: id -> site, class)
//	    -> baseline.tsv       (verify-day1-table.sh: id -> measured class)
//	      -> GNU bash 5.3.15  (classify.sh: bash -n, the oracle)
//
// TestBashPPDivergenceTableTraceable walks the first link and fails if a row
// cites an id day1Cases does not carry, disagrees with it about the site, or
// cites a row that is not Class E. verify-day1-table.sh walks the second. So
// re-measuring the corpus and getting a different class breaks the build
// rather than quietly relicensing a divergence.

// bashppDivergenceRow is one published Class E row at which LangBashPP is
// permitted to parse differently from LangBash.
type bashppDivergenceRow struct {
	// corpusID names the row in bashpp-tests/tools/startsites/shapes.tsv, via
	// the day1Cases table in bashpp_startsites_test.go. It is the whole point
	// of the row: an allowed divergence that cannot be named is not allowed.
	corpusID string

	// shape is the shape as the design of record spells it, and as day1Cases
	// records it — the two are checked equal. Attribution is exact against it
	// (see bashppAttribute), so the row licenses this shape and no other; it
	// also drives the escape check.
	shape string

	// site is the start site the shape opens. Cross-checked against day1Cases.
	site StartSite

	// why records what stock bash 5.3 does with the shape today — the reason
	// the row is Class E and therefore needs licensing at all. It is prose for
	// the reader of a failure message, not an assertion.
	why string
}

// bashppAllowedDivergences is the closed set of Day-1 (P1) Class E rows.
//
// Class R Day-1 shapes are deliberately ABSENT: `x := f()`, `x, y := f()`,
// `f(1, 2)`, `x.y.z()`, `clear(m)` and `n := len(s)` are all bash syntax errors
// today, so they need no row, no escape and no license. They are reached by
// rule 3 instead.
//
// StartGoIf is also absent, and its absence is load-bearing. `if err != nil {`
// measures Class E, but its commit point needs an unbounded scan to the
// matching brace, so it is an open design question rather than scheduled work.
// Until it is answered, `if` is not a Day-1 site, nothing may diverge there,
// and adding a row here would license a divergence the design has not agreed
// to.
var bashppAllowedDivergences = []bashppDivergenceRow{
	// var / const / type — every one of these is an ordinary command word in
	// bash today (`type` is even a builtin), so every one needs a row.
	{"decl-var-assign", "var x = 1", StartVar, "simple_command: command `var` with arguments"},
	{"decl-var-typed", "var x int = 1", StartVar, "simple_command: command `var` with arguments"},
	{"decl-var-bare", "var x int", StartVar, "simple_command: command `var` with arguments"},
	{"decl-const", "const K = 2", StartConst, "command `const` with arguments"},
	{"decl-const-typed", "const K int = 2", StartConst, "command `const` with arguments"},
	{"decl-type-alias", "type ID = string", StartTypeDecl, "command `type` (a bash builtin) with arguments"},
	{"prefix-type-struct", "type T struct {", StartTypeDecl, "a complete simple_command; `{` is an ordinary word in argument position"},

	// The := site splits across both classes. Only the Class E half is listed;
	// the Class R half is free and unlisted, which is the distinction the
	// measured corpus produced and inspection cannot.
	{"short-scalar-int", "x := 42", StartShortDecl, "command `x` with arguments `:=` and `42`"},
	{"short-scalar-string", `x := "hello"`, StartShortDecl, "command `x` with arguments `:=` and `\"hello\"`"},
	{"short-composite-map", `m := map[string]int{"a": 1}`, StartShortDecl, "words plus brace expansion"},
	{"short-composite-slice", "s := []int{1, 2, 3}", StartShortDecl, "words plus brace expansion"},
	{"short-composite-struct", `g := Gopher{Name: "x"}`, StartShortDecl, "words plus brace expansion"},
	{"short-index-generic", "f := Max[int]", StartShortDecl, "`[int]` is a glob character class"},
	{"short-multi-literal", "x, y := 1, 2", StartShortDecl, "a literal tuple has no parens: command `x,` with arguments"},
	{"short-multi-three", "x, y, z := 1, 2, 3", StartShortDecl, "a literal tuple has no parens: command `x,` with arguments"},
}

// bashppShapeBoundary reports whether rest — the source that immediately
// follows a published shape at a hit — ENDS that shape rather than continuing
// it into a different one.
//
// It is what makes attribution exact rather than merely prefix-shaped. Without
// it, `var x = 1` would attribute `var x = 1 extra` (a four-word command, a
// different shape) and `x := 42` would attribute `x := 421` (a different
// literal). Both are shapes nobody measured, so neither may borrow a row.
func bashppShapeBoundary(rest string) bool {
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return true
	}
	switch rest[0] {
	// The shell terminators, plus `#`: past any of these the command — and so
	// the shape — is over, and what follows is somebody else's problem.
	case ';', '&', '|', '\n', '\r', '#', ')':
		return true
	}
	return false
}

// bashppAttribute returns the published rows that attribute hit h EXACTLY: same
// start site, and the row's shape spans the whole command at the hit.
//
// Sharing a start site is deliberately not enough. `x := 42` and `x := <-ch`
// both open StartShortDecl and both measure Class E, but only the first is
// published; licensing the second because the first exists would let an
// unmeasured shape diverge under a row that does not describe it, which is the
// closed-row contract failing open. Returning no row is the fail-safe answer:
// the caller reports the divergence as unlisted and the gate fails.
func bashppAttribute(h bashppSiteHit) []bashppDivergenceRow {
	var out []bashppDivergenceRow
	for _, row := range bashppAllowedDivergences {
		if row.site != h.match.Site {
			continue
		}
		rest, ok := strings.CutPrefix(h.src, row.shape)
		if !ok || !bashppShapeBoundary(rest) {
			continue
		}
		out = append(out, row)
	}
	return out
}

// bashppSiteHit is one recognized start site inside one corpus input: where it
// fired, what the recognizer said, and the source it saw.
type bashppSiteHit struct {
	offset uint
	src    string // the source from the command position, bounded
	match  StartSiteMatch
}

func (h bashppSiteHit) String() string {
	return fmt.Sprintf("offset %d: site %v class %v, source %q",
		h.offset, h.match.Site, h.match.Class, h.src)
}

// bashppCommandSites reports every Bash++ start site recognized at a REAL bash
// command position of src.
//
// The positions are taken from the LangBash parse rather than guessed, which
// is what makes the attribution honest: a *CallExpr's first argument is
// precisely the command word position, which is the only place the dispatch in
// parser.go's gotStmtPipe will consult RecognizeStartSite. Compound commands
// are not scanned because no Day-1 site opens at one — `if` is deliberately not
// a Day-1 site, see bashppAllowedDivergences.
func bashppCommandSites(f *File, src string) []bashppSiteHit {
	var out []bashppSiteHit
	Walk(f, func(n Node) bool {
		call, ok := n.(*CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		off := call.Args[0].Pos().Offset()
		if off >= uint(len(src)) {
			return true
		}
		rest := src[off:]
		if m := RecognizeStartSite(rest); m.Site != StartNone {
			out = append(out, bashppSiteHit{offset: off, src: boundedSrc(rest), match: m})
		}
		return true
	})
	return out
}

// bashppLineSites is the attribution used for inputs LangBash REJECTS, where
// there is no bash AST to take command positions from. It scans the start of
// each line, which is how every shape in the measured corpus is written.
//
// It is deliberately an over-approximation. It only ever gates rule 3, whose
// ground — shapes bash already rejects — is purely additive and carries no
// compatibility risk; the job here is to catch an acceptance that no start site
// explains, not to police a contract.
func bashppLineSites(src string) []bashppSiteHit {
	var out []bashppSiteHit
	var off uint
	for _, line := range strings.SplitAfter(src, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		start := off + uint(len(line)-len(trimmed))
		if m := RecognizeStartSite(trimmed); m.Site != StartNone {
			out = append(out, bashppSiteHit{offset: start, src: boundedSrc(trimmed), match: m})
		}
		off += uint(len(line))
	}
	return out
}

func boundedSrc(s string) string {
	if len(s) > maxLookahead {
		s = s[:maxLookahead]
	}
	return strings.TrimRight(s, "\n")
}

// bashppLicense decides whether a set of recognized sites licenses a
// divergence, and names the rows that do.
//
// A hit is licensed only when BOTH hold:
//
//   - it is Class E — Class R ground is bash syntax errors, which need no
//     license and get none; and
//   - a published row attributes it exactly, per bashppAttribute.
//
// The second condition is the closed-row contract, and it is the reason this
// function exists at all. An earlier form licensed any Class E hit that merely
// SHARED a start site with some published row, which let a shape nobody
// measured — a new := right-hand side, say — diverge and pass under a row that
// describes a different shape. A table that licenses shapes it does not list is
// not a table.
//
// So an unmeasured shape at an already-listed site fails exactly like a shape
// at an unlisted site does, and the remedy is the same: measure it into
// bashpp-tests/tools/startsites, add the row, and let
// TestBashPPDivergenceTableTraceable check the citation. Widening the matcher
// is not a remedy.
//
// Any hit that is not licensed, and the absence of hits altogether, are both
// reported so the caller can fail: an unnamed divergence is a failure by
// construction.
func bashppLicense(hits []bashppSiteHit) (named []bashppDivergenceRow, unlicensed []bashppSiteHit) {
	for _, h := range hits {
		rows := bashppAttribute(h)
		if h.match.Class != ClassE || len(rows) == 0 {
			unlicensed = append(unlicensed, h)
			continue
		}
		named = append(named, rows...)
	}
	return named, unlicensed
}

func bashppRowIDs(rows []bashppDivergenceRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.corpusID)
	}
	return ids
}

// bashppEscapes returns the published shell escapes for a row, applied to its
// own shape.
//
// The design of record publishes two forms for every Class E Day-1 row:
// `command <shape>`, and quoting the first word. Both work the same way — they
// make the position not a Bash++ command position at all — which is why they
// are constructed here rather than transcribed: a row that cannot be escaped
// by the published forms is a row that should not have been claimed.
func bashppEscapes(row bashppDivergenceRow) []string {
	out := []string{"command " + row.shape}
	if word, rest, ok := strings.Cut(row.shape, " "); ok {
		out = append(out, `"`+word+`" `+rest, `'`+word+`' `+rest)
	}
	return out
}

// TestBashPPDivergenceTableTraceable walks the first link of the traceability
// chain: every allowed divergence must name a Day-1 row that exists, agrees
// about the start site, and is Class E.
//
// It also asserts the reverse inclusion. A Class E Day-1 row with no entry here
// would not silently permit anything — an unnamed divergence fails — but it
// WOULD mean the published contract and the allowlist had drifted apart, with
// the allowlist quietly the stricter of the two. Either direction of drift is a
// bug in the table, so both fail.
func TestBashPPDivergenceTableTraceable(t *testing.T) {
	t.Parallel()

	byID := make(map[string]startSiteCase, len(day1Cases))
	for _, tc := range day1Cases {
		byID[tc.corpusID] = tc
	}

	listed := make(map[string]bool, len(bashppAllowedDivergences))
	for _, row := range bashppAllowedDivergences {
		t.Run(row.corpusID, func(t *testing.T) {
			if listed[row.corpusID] {
				t.Fatalf("duplicate row for corpus id %q", row.corpusID)
			}
			tc, ok := byID[row.corpusID]
			if !ok {
				t.Fatalf("row cites corpus id %q, which day1Cases does not carry; "+
					"an allowed divergence must be traceable to the measured corpus",
					row.corpusID)
			}
			if tc.class != ClassE {
				t.Fatalf("row cites %q, which day1Cases records as Class %v; "+
					"only Class E rows may license a divergence, because only "+
					"Class E shapes are ones bash accepts today",
					row.corpusID, tc.class)
			}
			if tc.want != row.site {
				t.Fatalf("row cites %q with site %v, but day1Cases records site %v",
					row.corpusID, row.site, tc.want)
			}
			if tc.src != row.shape {
				t.Fatalf("row cites %q with shape %q, but day1Cases records %q",
					row.corpusID, row.shape, tc.src)
			}
			// The recognizer must agree, or the row names a shape the parser
			// will never actually claim.
			got := RecognizeStartSite(row.shape)
			if got.Site != row.site || got.Class != ClassE {
				t.Fatalf("RecognizeStartSite(%q) = %v/%v, but the row claims %v/E",
					row.shape, got.Site, got.Class, row.site)
			}
		})
		listed[row.corpusID] = true
	}

	for _, tc := range day1Cases {
		if tc.class == ClassE && !listed[tc.corpusID] {
			t.Errorf("day1Cases carries Class E row %q (%q) with no entry in "+
				"bashppAllowedDivergences; every published Class E row must be "+
				"listed, or the allowlist and the contract have drifted",
				tc.corpusID, tc.src)
		}
		if tc.class == ClassR && listed[tc.corpusID] {
			t.Errorf("Class R row %q is listed as an allowed divergence; "+
				"bash rejects that shape, so it needs no license", tc.corpusID)
		}
	}
}

// TestBashPPEscapesRestoreBash asserts the other half of what a Class E row
// promises. Listing a divergence is only acceptable because the published
// escape gives the shape back, so the escape is exercised rather than assumed.
//
// Two things are checked per escape: the recognizer must not fire on it, and
// the escaped input must parse identically under both languages. The second is
// the one that keeps meaning: it is what a maintainer of an existing script
// actually does when Bash++ claims their command name.
func TestBashPPEscapesRestoreBash(t *testing.T) {
	t.Parallel()

	for _, row := range bashppAllowedDivergences {
		for _, escaped := range bashppEscapes(row) {
			t.Run(row.corpusID+"/"+escaped, func(t *testing.T) {
				if got := RecognizeStartSite(escaped); got.Site != StartNone {
					t.Fatalf("escape %q was claimed as %v; the published escape must win",
						escaped, got.Site)
				}
				if diff := bashppParseDiff(t, escaped); diff != "" {
					t.Fatalf("escaped input %q does not parse identically: %s", escaped, diff)
				}
			})
		}
	}
}

// TestBashPPLicenseRejectsUnlisted is the gate's self-test.
//
// The allowlist machinery is dormant while the dispatch is absent: today no
// input diverges, so TestBashPPMatchesBash never reaches the licensing branch
// and a broken allowlist would look exactly like a working one. That is the
// same shape of failure the bounded-lookahead work ran into three times — a
// conservative answer that looks like success — so the decision function is
// exercised directly, on both verdicts.
func TestBashPPLicenseRejectsUnlisted(t *testing.T) {
	t.Parallel()

	t.Run("a published Class E shape is licensed", func(t *testing.T) {
		hits := []bashppSiteHit{{src: "x := 42", match: StartSiteMatch{Site: StartShortDecl, Class: ClassE, Bounded: true}}}
		named, unlicensed := bashppLicense(hits)
		if len(unlicensed) != 0 {
			t.Fatalf("published Class E site reported as unlicensed: %v", unlicensed)
		}
		if len(named) != 1 || named[0].corpusID != "short-scalar-int" {
			t.Fatalf("divergence named %v, want exactly [short-scalar-int]", bashppRowIDs(named))
		}
	})

	t.Run("class R is never licensed", func(t *testing.T) {
		hits := []bashppSiteHit{{src: "x := f()", match: StartSiteMatch{Site: StartShortDecl, Class: ClassR, Bounded: true}}}
		named, unlicensed := bashppLicense(hits)
		if len(named) != 0 || len(unlicensed) != 1 {
			t.Fatalf("Class R hit licensed by %v; only Class E rows may license a divergence",
				bashppRowIDs(named))
		}
	})

	t.Run("an unlisted shape at a listed site is never licensed", func(t *testing.T) {
		// The failure mode this whole function exists to prevent. StartShortDecl
		// is a listed site and `x := <-ch` measures Class E just as `x := 42`
		// does, but a channel receive is a P4 shape that no Day-1 row describes.
		// Sharing a site with a published row must not lend it that row's
		// license, or the closed set is not closed.
		hits := []bashppSiteHit{{src: "x := <-ch", match: StartSiteMatch{Site: StartShortDecl, Class: ClassE, Bounded: true}}}
		named, unlicensed := bashppLicense(hits)
		if len(named) != 0 || len(unlicensed) != 1 {
			t.Fatalf("unmeasured shape %q licensed by %v; a row licenses its own "+
				"shape, not every shape at its site", hits[0].src, bashppRowIDs(named))
		}
	})

	t.Run("a shape a row merely prefixes is never licensed", func(t *testing.T) {
		// `x := 421` opens with the published shape `x := 42` byte for byte, and
		// `var x = 1 extra` opens with `var x = 1`. Neither IS the published
		// shape, so prefix-matching alone would license two shapes nobody
		// measured. This is what bashppShapeBoundary is for.
		for _, src := range []string{"x := 421", "var x = 1 extra"} {
			site := StartShortDecl
			if strings.HasPrefix(src, "var") {
				site = StartVar
			}
			hits := []bashppSiteHit{{src: src, match: StartSiteMatch{Site: site, Class: ClassE, Bounded: true}}}
			named, unlicensed := bashppLicense(hits)
			if len(named) != 0 || len(unlicensed) != 1 {
				t.Errorf("input %q licensed by %v; it only starts like a published "+
					"shape, it is not one", src, bashppRowIDs(named))
			}
		}
	})

	t.Run("a published shape ending at a terminator is licensed", func(t *testing.T) {
		// The other side of the boundary rule: exactness must not mean the shape
		// has to be the entire input. A published shape followed by `;`, `&`,
		// `|`, a newline or a comment is still that shape, and a real corpus
		// input is usually longer than one command.
		for _, src := range []string{"x := 42", "x := 42; echo done", "x := 42 # note", "x := 42 | cat"} {
			hits := []bashppSiteHit{{src: src, match: StartSiteMatch{Site: StartShortDecl, Class: ClassE, Bounded: true}}}
			named, unlicensed := bashppLicense(hits)
			if len(unlicensed) != 0 || len(named) != 1 || named[0].corpusID != "short-scalar-int" {
				t.Errorf("input %q named %v (unlicensed %v), want exactly [short-scalar-int]",
					src, bashppRowIDs(named), unlicensed)
			}
		}
	})

	t.Run("an unlisted site is never licensed", func(t *testing.T) {
		// StartGoIf is Class E in the corpus but carries no row, because its
		// commit point is an unanswered design question. Class alone must not
		// be enough; the row is what licenses.
		hits := []bashppSiteHit{{src: "if err != nil {", match: StartSiteMatch{Site: StartGoIf, Class: ClassE}}}
		named, unlicensed := bashppLicense(hits)
		if len(named) != 0 || len(unlicensed) != 1 {
			t.Fatalf("unlisted site %v licensed by %v; a divergence not in the table is a failure",
				StartGoIf, bashppRowIDs(named))
		}
	})

	t.Run("no site at all is no license", func(t *testing.T) {
		named, unlicensed := bashppLicense(nil)
		if len(named) != 0 || len(unlicensed) != 0 {
			t.Fatalf("empty hit list produced %v / %v", bashppRowIDs(named), unlicensed)
		}
	})
}

// TestBashPPCommandSitesFindShapes proves the attribution used by
// TestBashPPMatchesBash actually sees the shapes it is supposed to see.
//
// Without this the gate could pass vacuously in the worst way: if
// bashppCommandSites returned nothing for a genuine Class E shape, a future
// divergence there would be reported as UNLISTED and the implementer would go
// looking for a parser bug that does not exist. Every published shape that
// bash accepts must therefore be found at a real bash command position.
func TestBashPPCommandSitesFindShapes(t *testing.T) {
	t.Parallel()

	for _, row := range bashppAllowedDivergences {
		t.Run(row.corpusID, func(t *testing.T) {
			f, err := bashppParse(LangBash, row.shape)
			if err != nil {
				// A Class E shape is one bash accepts; if this engine cannot
				// parse it, the row and the engine disagree.
				t.Fatalf("LangBash rejected Class E shape %q: %v", row.shape, err)
			}
			hits := bashppCommandSites(f, row.shape)
			if len(hits) == 0 {
				t.Fatalf("no start site found at any command position of %q; "+
					"a real divergence here would be misreported as unlisted", row.shape)
			}
			named, unlicensed := bashppLicense(hits)
			if len(unlicensed) != 0 {
				t.Fatalf("published shape %q produced unlicensed hits %v", row.shape, unlicensed)
			}
			if len(named) == 0 {
				t.Fatalf("published shape %q licensed nothing", row.shape)
			}
		})
	}
}
