// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import "fmt"

// This file holds the Bash++ P1 ("Day-1") typed nodes. It is deliberately a
// separate file from nodes.go: the nodes can be declared, reviewed and merged
// without touching a single line the certification workstream owns, which
// keeps the shared-file diff down to the dispatch call itself.
//
// The P1 command-position dispatch is wired into parser.go, so every node below
// except BashPPIf is now constructed from real input: the var/const/type
// declarations, the := short declarations, the Go-form call, import, func,
// return and defer. BashPPIf alone stays unconstructed by design — brace-form
// `if` cannot be decided by the bounded-lookahead recognizer and is deferred;
// see bashpp_braceif_decision.go. The node, its enum and its interpreter stub
// are kept ready for the different mechanism a later sprint will need.
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
	StartImport    // import "path" · import alias "path"
	StartFunc      // func name(a int) int { … }
	StartDefer     // defer f(x)
	StartReturn    // return · return a, b (only inside a func body)
	StartFuncLit   // func(a int) int { … }(1) — a function literal
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
	case StartImport:
		return "import"
	case StartFunc:
		return "func"
	case StartDefer:
		return "defer"
	case StartReturn:
		return "return"
	case StartFuncLit:
		return "funclit"
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
	case "import":
		*s = StartImport
	case "func":
		*s = StartFunc
	case "defer":
		*s = StartDefer
	case "return":
		*s = StartReturn
	case "funclit":
		*s = StartFuncLit
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
	Alias    bool      // whether a type declaration uses the `=` alias form
	Init     []*Word   // the initializer, or nil for a bare declaration
	// StructFields is non-empty for the closed Bash# struct declaration
	// surface, `type T struct { Name string; ... }`. DeclType is the `struct`
	// literal in that form; the braces retain their source positions.
	StructFields []*BashPPField
	// EnumMembers is non-empty for the closed Bash# enum declaration
	// surface, `type Color enum { Red; Green }`. Members retain their source
	// positions so diagnostics and typed JSON never need to split source text.
	EnumMembers []*Lit
	Lbrace      Pos
	Rbrace      Pos

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

// BashPPAssign is a Go-form assignment used by Bash# deep-readonly checks.
// Target and Value remain words so the AST preserves quote structure and byte
// positions; the interpreter parses their deliberately small expression
// grammar only after the node has passed the Bash++/POSIX runtime gates.
type BashPPAssign struct {
	Target *Word
	Eq     Pos
	Value  *Word
}

func (a *BashPPAssign) Pos() Pos { return a.Target.Pos() }
func (a *BashPPAssign) End() Pos { return a.Value.End() }

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

	// Call is set when the right-hand side is a Go-form call, e.g.
	// `x := f(1)` or `config, err := readConfig("c")`. It carries the parsed
	// call so the interpreter can invoke a typed function and bind its results
	// to Lhs positionally, rather than re-deriving the call from Rhs's text.
	// It is nil for every scalar/tuple/composite right-hand side.
	Call *BashPPCall

	// FuncLit is set when the right-hand side is a function literal bound to
	// a name, `greet := func(who string) { … }`. Rhs is empty then: a closure
	// has no word spelling to re-expand, and the literal's own body is the
	// value. When the literal is immediately invoked — `n := func() int { …
	// }()` — Call carries the invocation and FuncLit stays nil, because what
	// is bound is the call's result rather than the function.
	FuncLit *BashPPFuncLit

	// MethodValue is set for a selector captured as a value, `f := v.M`.
	// Rhs retains the original word for compatibility; this field records the
	// selector structure so neither the printer nor interpreter must split
	// source text to rediscover it.
	MethodValue []*Lit
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
	if d.FuncLit != nil {
		return d.FuncLit.End()
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
	Fun  []*Lit  // the selector chain: x.y.z is three literals
	Args []*Word // the arguments, unevaluated

	// ArgNames names the trailing named arguments in source order. Since the
	// grammar requires positional arguments first, Args[:len(Args)-len(ArgNames)]
	// are positional and each remaining Args entry pairs with an ArgNames entry.
	// This compact form avoids nil placeholders in the public/JSON AST.
	ArgNames []*Lit

	// FuncLit is set when the callee is a function literal rather than a
	// name, which is what an immediately invoked literal — `func(n int) {
	// … }(1)` — is. Fun is empty exactly then, so the two are alternatives
	// and never both set; a reader asks which one is nil rather than
	// re-deriving the shape from the source.
	FuncLit *BashPPFuncLit

	// Ellipsis is the position of the `...` in `f(xs...)`, which passes a
	// slice to a variadic parameter instead of one more argument. Go allows it
	// only on the final argument, so one position on the call is enough.
	Ellipsis Pos

	// PointerMethodExpr records the exact `(*T).M` callee spelling. Fun still
	// contains T,M so selector resolution remains uniform.
	PointerMethodExpr bool
	MethodExprLparen  Pos
	MethodExprRparen  Pos

	Lparen Pos
	Rparen Pos
}

// BashPPCommandCall is a shell command whose final argument is the result of a
// typed Bash# call, as in `printf '%s\n' label(Green)`. Keeping the outer
// command and inner call as nodes avoids inventing a shell expansion spelling
// for a typed return value.
type BashPPCommandCall struct {
	Before []*Word
	Call   *BashPPCall
}

func (c *BashPPCommandCall) Pos() Pos {
	if len(c.Before) > 0 {
		return c.Before[0].Pos()
	}
	return c.Call.Pos()
}
func (c *BashPPCommandCall) End() Pos { return c.Call.End() }

// BashPPImport imports one or more standard-library packages into the
// Runner-local Bash++ namespace.
type BashPPImport struct {
	Site     StartSite
	Class    SiteClass
	Kw       *Lit
	Alias    *Lit                // nil means the package's declared name (single form)
	Path     *DblQuoted          // non-nil in the single form
	Comments []Comment           // comments between the keyword and grouped form
	Specs    []*BashPPImportSpec // non-nil in the grouped form
	Last     []Comment           // comments before the closing parenthesis
	Lparen   Pos
	Rparen   Pos
}

func (i *BashPPImport) Pos() Pos { return i.Kw.Pos() }
func (i *BashPPImport) End() Pos {
	if i.Path != nil {
		return i.Path.End()
	}
	return posAddCol(i.Rparen, 1)
}

// BashPPImportSpec is one exact Go import specification. Alias may be a Go
// identifier, _ or .; nil requests the package's declared name.
type BashPPImportSpec struct {
	Comments []Comment
	Alias    *Lit
	Path     *DblQuoted
}

func (s *BashPPImportSpec) Pos() Pos {
	if s.Alias != nil {
		return s.Alias.Pos()
	}
	return s.Path.Pos()
}
func (s *BashPPImportSpec) End() Pos { return s.Path.End() }

func (c *BashPPCall) Pos() Pos {
	if c.MethodExprLparen.IsValid() {
		return c.MethodExprLparen
	}
	if len(c.Fun) > 0 {
		return c.Fun[0].Pos()
	}
	if c.FuncLit != nil {
		return c.FuncLit.Pos()
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

// BashPPSwitch is the exhaustive switch form admitted inside a typed Bash#
// function. Its expression and case members remain syntax nodes so positions,
// Walk, printing, and typed JSON all describe the source rather than a lowered
// representation.
type BashPPSwitch struct {
	Switch Pos
	Expr   *Word
	Lbrace Pos
	Arms   []*BashPPSwitchArm
	Rbrace Pos
}

func (s *BashPPSwitch) Pos() Pos { return s.Switch }
func (s *BashPPSwitch) End() Pos { return posAddCol(s.Rbrace, 1) }

// BashPPSwitchArm is one `case Member:` or `default:` arm.
type BashPPSwitchArm struct {
	Case   Pos // position of case/default
	Member *Lit
	Colon  Pos
	Stmts  []*Stmt
	Last   []Comment
}

func (a *BashPPSwitchArm) Pos() Pos { return a.Case }
func (a *BashPPSwitchArm) End() Pos {
	if len(a.Stmts) > 0 {
		return a.Stmts[len(a.Stmts)-1].End()
	}
	return posAddCol(a.Colon, 1)
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

// BashPPField is one group of a Go-form parameter or result list: `a, b int`,
// a lone untyped parameter name, or a bare result type.
//
// Names carries the identifiers a group declares — several when a type is
// shared, as in `a, b int`. FieldType is the group's declared type, or nil for an
// untyped parameter (a Bash++ extension over Go, `func f(a, b)`). An unnamed
// result — `func f() (int, error)` — is a group with an empty Names and the
// type in FieldType, which is what keeps the result arity queryable from the tree
// without re-reading whether each word was a name or a type.
type BashPPField struct {
	Names     []*Lit // the declared identifiers, empty for an unnamed result type
	FieldType *Lit   // the declared type, or nil for an untyped parameter
	Default   *Word  // the value after `=`, nil for a required parameter
	Equals    Pos    // position of `=` when Default is non-nil

	// Ellipsis is the position of the `...` in a variadic parameter group,
	// `func f(head string, rest ...int)`, and is invalid for every other
	// group. It is a POSITION rather than a bool because the printer must put
	// the dots back where they were written, and because Go's only-final rule
	// is reported against the offending group's own source column.
	//
	// FieldType stays the ELEMENT type (`int` above), as it is in go/ast: the
	// parameter's own type is a slice of it, and spelling the element type is
	// what lets the arity check and the diagnostics talk about the same name
	// the script wrote.
	Ellipsis Pos
}

// Variadic reports whether the group is the `...T` form.
func (f *BashPPField) Variadic() bool { return f.Ellipsis.IsValid() }

func (f *BashPPField) Pos() Pos {
	if len(f.Names) > 0 {
		return f.Names[0].Pos()
	}
	if f.Ellipsis.IsValid() {
		return f.Ellipsis
	}
	if f.FieldType != nil {
		return f.FieldType.Pos()
	}
	return Pos{}
}
func (f *BashPPField) End() Pos {
	if f.Default != nil {
		return f.Default.End()
	}
	if f.FieldType != nil {
		return f.FieldType.End()
	}
	if len(f.Names) > 0 {
		return f.Names[len(f.Names)-1].End()
	}
	return Pos{}
}

// BashPPFuncDecl is a Go-form typed function declaration:
//
//	func name(a int, b string) (int, error) { … }
//
// Every shape reaching this node is Class R — `func name(…)` is two command
// words before a `(`, which bash rejects, so claiming it takes nothing away
// from a working script. That is why the signature may be parsed forward
// without a transaction: a malformed body is a bash syntax error either way.
type BashPPFuncDecl struct {
	Kw       *Lit            // the literal "func"
	Name     *Lit            // the declared function name
	Receiver *BashPPReceiver // nil for an ordinary function
	Params   []*BashPPField  // the parameter groups, in source order
	Results  []*BashPPField  // the result groups, or nil when there are none
	Body     *Block          // the braced body

	Lparen    Pos // ( opening the parameter list
	Rparen    Pos // ) closing the parameter list
	ResLparen Pos // ( opening a parenthesised result list, else invalid
	ResRparen Pos // ) closing a parenthesised result list, else invalid
}

// BashPPReceiver is the single receiver of a method declaration. Basic
// named-type receivers are the Sprint 114 surface; embedding, interfaces and
// generic receivers remain deliberately outside this node's grammar.
type BashPPReceiver struct {
	Name     *Lit
	RecvType *Lit
	Pointer  bool
	Lparen   Pos
	Rparen   Pos
}

func (r *BashPPReceiver) Pos() Pos { return r.Lparen }
func (r *BashPPReceiver) End() Pos { return posAddCol(r.Rparen, 1) }

func (d *BashPPFuncDecl) Pos() Pos { return d.Kw.Pos() }
func (d *BashPPFuncDecl) End() Pos {
	if d.Body != nil {
		return d.Body.End()
	}
	return posAddCol(d.Rparen, 1)
}

// BashPPFuncLit is a Go function literal: an unnamed function written where a
// value is expected.
//
//	greet := func(who string) { echo "hi $who" }
//	func(n int) { echo $n }(1)
//	defer func() { echo done }()
//	return func(extra int) int { return $((base + extra)) }
//
// WHY IT IS NOT A COMMAND. A bare literal is not a statement in Go either; a
// literal always appears as a value — bound by `:=`, invoked immediately,
// deferred, or returned. Each of those sites owns a field pointing here, so
// the tree records WHICH site the literal occupied instead of leaving a
// free-floating node whose meaning a reader would have to infer from context.
//
// THE ONE SHAPE THAT IS NOT CLAIMED. `func() { … }` at a command position is
// the bash function definition of a function NAMED `func`, and it is legal
// shell today. Only the trailing `()` after the matching `}` tells the two
// apart, which is unbounded lookahead of exactly the kind
// bashpp_startsites.go forbids. So a command-position literal must carry a
// parameter — `func(n int) { … }(1)` — and the parameterless invocation is
// spelled `_ := func() { … }()`, where the `:=` prefix already commits the
// region. Every other literal site is unambiguous from its prefix.
type BashPPFuncLit struct {
	Kw      *Lit           // the literal "func"
	Params  []*BashPPField // the parameter groups, in source order
	Results []*BashPPField // the result groups, or nil when there are none
	Body    *Block         // the braced body

	Lparen    Pos // ( opening the parameter list
	Rparen    Pos // ) closing the parameter list
	ResLparen Pos // ( opening a parenthesised result list, else invalid
	ResRparen Pos // ) closing a parenthesised result list, else invalid
}

func (l *BashPPFuncLit) Pos() Pos { return l.Kw.Pos() }
func (l *BashPPFuncLit) End() Pos {
	if l.Body != nil {
		return l.Body.End()
	}
	if l.ResRparen.IsValid() {
		return posAddCol(l.ResRparen, 1)
	}
	return posAddCol(l.Rparen, 1)
}

// BashPPReturn is a Go-form return inside a func body: `return`, `return a, b`.
//
// It is only ever constructed inside a Bash++ func body, where the parser
// tracks that it is within a committed Go region; a `return` anywhere else
// stays the ordinary shell builtin, which is Class E. Results is nil for a bare
// return, which yields the function's named results (or its last status when it
// declares none).
type BashPPReturn struct {
	Kw      *Lit    // the literal "return"
	Results []*Word // the returned values, or nil for a bare return

	// FuncLit is set when the single returned value is a function literal,
	// `return func(n int) int { … }`. That is how a closure ESCAPES the
	// function that built it — the factory idiom — and it needs its own field
	// because a literal has no word spelling for Results to hold.
	FuncLit *BashPPFuncLit
}

func (r *BashPPReturn) Pos() Pos { return r.Kw.Pos() }
func (r *BashPPReturn) End() Pos {
	if len(r.Results) > 0 {
		return r.Results[len(r.Results)-1].End()
	}
	if r.FuncLit != nil {
		return r.FuncLit.End()
	}
	return r.Kw.End()
}

// BashPPDefer is a Go-form deferred call: `defer f(x)`.
//
// `defer` is an ordinary command word in bash, so `defer cleanup` runs a
// command today and must keep doing so (Class E). Only the Go-call form
// `defer f(x)` — already a bash syntax error, hence Class R — is claimed, which
// is what lets an existing script that shells out to a program named `defer`
// keep working untouched.
type BashPPDefer struct {
	Kw   *Lit        // the literal "defer"
	Call *BashPPCall // the deferred call
}

func (d *BashPPDefer) Pos() Pos { return d.Kw.Pos() }
func (d *BashPPDefer) End() Pos {
	if d.Call != nil {
		return d.Call.End()
	}
	return d.Kw.End()
}

// The Command marker methods. Declaring them here rather than in nodes.go is
// what lets this whole file merge without touching a certification-owned file.
func (*BashPPDecl) commandNode()        {}
func (*BashPPShortDecl) commandNode()   {}
func (*BashPPAssign) commandNode()      {}
func (*BashPPCall) commandNode()        {}
func (*BashPPCommandCall) commandNode() {}
func (*BashPPIf) commandNode()          {}
func (*BashPPSwitch) commandNode()      {}
func (*BashPPImport) commandNode()      {}
func (*BashPPFuncDecl) commandNode()    {}
func (*BashPPReturn) commandNode()      {}
func (*BashPPDefer) commandNode()       {}
