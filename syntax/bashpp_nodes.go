// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import "fmt"

// This file holds the Bash++ P1 ("Day-1") typed nodes. It is deliberately a
// separate file from nodes.go: the nodes can be declared, reviewed and merged
// without touching a single line the certification workstream owns, which
// keeps the shared-file diff down to the dispatch call itself.
//
// None of these nodes can appear in a tree yet. Nothing constructs them until
// the command-position dispatch is wired into parser.go, which is held. They
// are compiled and unit-tested here so that when the wiring lands it is one
// call, not a design.
//
// Two rules from the design of record govern every node below, and both are
// easy to violate later:
//
//   - Classification is stored ONCE, here in the typed AST. No later phase
//     re-reads source text to decide whether a region is Go or shell. A node's
//     existence IS the decision.
//   - Go semicolon insertion applies only INSIDE a committed Go region. The
//     Site field records which start site opened the region so that rule has
//     something to key on.

// StartSite identifies the committed start site that opened a Bash++ region.
// It is recorded on every Bash++ node so that the region's provenance survives
// into the interpreter and the printer, rather than being re-derived.
type StartSite uint8

const (
	// StartNone means no Bash++ start site fired and the input stays shell.
	// It is the zero value on purpose: a node that never had its site set is
	// indistinguishable from shell, which fails safe.
	StartNone StartSite = iota

	StartVar       // var x = 1 · var x int = 1 · var x int
	StartConst     // const K = 2 · const K int = 2
	StartTypeDecl  // type T struct { … } · type ID = string
	StartShortDecl // x := 42 · x, y := f()
	StartGoCall    // f(1, 2) · x.y.z() · clear(m)
	StartGoIf      // if err != nil { … }
)

func (s StartSite) String() string {
	switch s {
	case StartVar:
		return "var"
	case StartConst:
		return "const"
	case StartTypeDecl:
		return "type"
	case StartShortDecl:
		return ":="
	case StartGoCall:
		return "call"
	case StartGoIf:
		return "if"
	}
	return "none"
}

// UnmarshalText is the inverse of [StartSite.String], and exists because
// sh/syntax/typedjson encodes a node's small enums by their string form so the
// wire format survives new values being added. Without the inverse, a tree
// containing a Bash++ node encodes cleanly and then fails to decode, which is
// the worst place to discover a missing method.
func (s *StartSite) UnmarshalText(b []byte) error {
	switch string(b) {
	case "none":
		*s = StartNone
	case "var":
		*s = StartVar
	case "const":
		*s = StartConst
	case "type":
		*s = StartTypeDecl
	case ":=":
		*s = StartShortDecl
	case "call":
		*s = StartGoCall
	case "if":
		*s = StartGoIf
	default:
		return fmt.Errorf("unknown Bash++ start site: %q", b)
	}
	return nil
}

// SiteClass is the compatibility class of a start site, as measured against
// stock bash 5.3 by bashpp-tests/tools/startsites and published in the design
// of record.
//
// The distinction is not cosmetic; it decides what happens to an unsupported
// form. A Class R shape is already a bash syntax error, so Bash++ may answer
// with a diagnostic — nothing legal is being taken away. A Class E shape runs
// today as an ordinary command, so an unsupported Class E form must fall back
// to the shell and must NEVER produce a diagnostic: emitting one would break a
// working script.
type SiteClass uint8

const (
	// ClassR — stock bash 5.3 REJECTS the shape. Purely additive.
	ClassR SiteClass = iota + 1
	// ClassE — stock bash 5.3 ACCEPTS the shape. Meaning-changing, and so it
	// carries a published table row, a near-miss fallback and a shell escape.
	ClassE
)

func (c SiteClass) String() string {
	switch c {
	case ClassR:
		return "R"
	case ClassE:
		return "E"
	}
	return "?"
}

// UnmarshalText is the inverse of [SiteClass.String]; see
// [StartSite.UnmarshalText] for why it exists.
func (c *SiteClass) UnmarshalText(b []byte) error {
	switch string(b) {
	case "R":
		*c = ClassR
	case "E":
		*c = ClassE
	default:
		return fmt.Errorf("unknown Bash++ site class: %q", b)
	}
	return nil
}

// BashPPDecl is a Go declaration in command position: var, const or type.
//
// It covers the three keyword-led Day-1 sites, which share a shape (keyword,
// name, optional type, optional value) and differ only in the keyword. They
// are one node rather than three because the interpreter treats them
// identically apart from mutability, which Kw already records.
//
// DeclType is spelled that way rather than the obvious `Type` because
// sh/syntax/typedjson reserves a `Type` key on every tagged node to carry the
// node's own type name, and builds its encoding struct by reflection: a node
// field called Type collides with it and panics reflect.StructOf. That is not
// a hypothetical — it is what wiring the dispatch turned up the first time a
// BashPPDecl could reach an encoder — and it is worth a line here because the
// collision is invisible until a tree containing one is serialized.
type BashPPDecl struct {
	Site     StartSite // StartVar, StartConst or StartTypeDecl
	Kw       *Lit      // the literal "var", "const" or "type" as written
	Name     *Lit      // the declared identifier
	DeclType *Lit      // the declared type, or nil when inferred
	Init     []*Word   // the initializer, or nil for a bare declaration

	// End_ is the end of the declaration, which for a multi-line type
	// declaration is the closing brace rather than the end of Init.
	End_ Pos
}

func (d *BashPPDecl) Pos() Pos { return d.Kw.Pos() }
func (d *BashPPDecl) End() Pos {
	if d.End_.IsValid() {
		return d.End_
	}
	if len(d.Init) > 0 {
		return d.Init[len(d.Init)-1].End()
	}
	if d.DeclType != nil {
		return d.DeclType.End()
	}
	return d.Name.End()
}

// BashPPShortDecl is a Go short variable declaration: x := 42, x, y := f().
//
// Lhs is a slice because the multi-value form is what makes several of these
// shapes Class R: a parenthesised call after := is already a bash syntax
// error, while the bare scalar form is not. Both spellings land here; Class
// records which one fired, so the compatibility contract is queryable from the
// tree instead of being re-derived from source.
type BashPPShortDecl struct {
	Lhs   []*Lit  // one name, or several for the tuple form
	Rhs   []*Word // the right-hand side, unevaluated
	Class SiteClass
	OpPos Pos // position of :=
}

func (d *BashPPShortDecl) Pos() Pos {
	if len(d.Lhs) > 0 {
		return d.Lhs[0].Pos()
	}
	return d.OpPos
}

func (d *BashPPShortDecl) End() Pos {
	if len(d.Rhs) > 0 {
		return d.Rhs[len(d.Rhs)-1].End()
	}
	return posAddCol(d.OpPos, 2)
}

// BashPPCall is a Go call expression in command position: f(1, 2), x.y.z().
//
// Every shape reaching this node is Class R — the parenthesis after a word is
// only legal in bash as a function definition, so `f(1, 2)` is already a
// syntax error and claiming it takes nothing away. This is the free
// disambiguator the whole Day-1 set leans on, and it is why `go build ./...`
// keeps running the Go toolchain while `go worker(a, b)` does not.
type BashPPCall struct {
	Fun    []*Lit  // the selector chain: x.y.z is three literals
	Args   []*Word // the arguments, unevaluated
	Lparen Pos
	Rparen Pos
}

func (c *BashPPCall) Pos() Pos {
	if len(c.Fun) > 0 {
		return c.Fun[0].Pos()
	}
	return c.Lparen
}
func (c *BashPPCall) End() Pos { return posAddCol(c.Rparen, 1) }

// BashPPIf is a Go brace-form if: if err != nil { … }.
//
// This is the one Day-1 site whose commit point needs a completing context
// rather than a prefix. A shell `if` may legally end its condition with `{`
// and continue with `then`, so the brace alone does not decide. The absence of
// `then` after the matching `}` is what commits, which means the recognizer
// cannot answer from a bounded prefix and the parser must reach the closing
// brace before classifying. Recorded here because it is the one place the
// bounded-lookahead property below does not hold.
type BashPPIf struct {
	If   Pos
	Cond []*Word // the condition, unevaluated
	Then *Block  // the braced body
	Else Command // an *BashPPIf for else-if, a *Block for else, or nil
}

func (i *BashPPIf) Pos() Pos { return i.If }
func (i *BashPPIf) End() Pos {
	if i.Else != nil {
		return i.Else.End()
	}
	if i.Then != nil {
		return i.Then.End()
	}
	return i.If
}

// The Command marker methods. Declaring them here rather than in nodes.go is
// what lets this whole file merge without touching a certification-owned file.
func (*BashPPDecl) commandNode()      {}
func (*BashPPShortDecl) commandNode() {}
func (*BashPPCall) commandNode()      {}
func (*BashPPIf) commandNode()        {}
