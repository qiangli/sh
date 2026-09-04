// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Parsing of the Bash++ function sites: the P3-A ("typed functions")
// command-position declarations — `func name(…) … { … }`, `defer f(x)` and
// the func-body `return` — and the P3-B additions, function LITERALS and
// VARIADIC parameter lists.
//
// THE FOUR LITERAL SITES, and the one that is not claimed. A literal appears
// wherever a value does: bound by `:=`, invoked immediately, deferred, or
// returned. Each is claimed on a prefix bash rejects — `f := func(`,
// `defer func(`, `return func(`, and a command-position `func(` whose
// parameter list is NOT empty. The exception is `func() { … }` at a command
// position, which is the bash definition of a function named `func` and stays
// shell: only the `(` after the matching `}` could tell the two apart, and
// that is unbounded lookahead. See [recognizeFuncLit].
//
// WHY THESE COMMIT FORWARD RATHER THAN TRANSACTIONALLY. The keyword-led
// declarations in bashpp_decl.go consume nothing on speculation because they
// are Class E — bash runs their near-misses as ordinary commands, so a wrong
// guess would change a working script. A `func name(` is different: it is two
// command words before a `(`, which bash rejects outright (Class R). There is
// therefore no working script to protect, and the region may be committed the
// moment the shape is recognized, exactly as `if`/`for`/`while` commit on their
// keyword. A malformed body becomes a parse error, which is what bash produces
// too.
//
// `defer` splits the way `:=` does: `defer cleanup` is a Class E command bash
// runs today and must stay shell, while `defer f(x)` is the Class R call form.
// Only the call form is claimed, and it is claimed transactionally through the
// same [Parser.bashppParenForm] machinery the bare call uses, so an unsupported
// argument shape rewinds to the shell rather than diagnosing.

// bashppFuncForm parses a Go-form function declaration once the ordinary call
// parser has collected `func name` and stopped at the opening `(`. It returns
// nil to leave the command to the shell, and otherwise a fully parsed
// [BashPPFuncDecl]; because the shape is Class R, a malformation past the
// commit point is reported as a parse error rather than rewound.
func (p *Parser) bashppFuncForm(ce *CallExpr) Command {
	if ce == nil || len(ce.Assigns) != 0 || p.tok != leftParen {
		return nil
	}
	if len(ce.Args) == 1 {
		kw := bashppBareLit(ce.Args[0])
		// Use source positions rather than p.spaced here: callExpr has already
		// advanced into the token, so the lexer spacing bit describes the next
		// boundary. A receiver requires whitespace after func; func(...) stays
		// the literal/classic-function ambiguity handled elsewhere.
		if kw == nil || kw.Value != "func" || p.pos.Offset() == kw.End().Offset() {
			return nil
		}
		return p.bashppMethodForm(kw)
	}
	if len(ce.Args) != 2 {
		return nil
	}
	kw := bashppBareLit(ce.Args[0])
	name := bashppBareLit(ce.Args[1])
	if kw == nil || kw.Value != "func" || name == nil || !bashppIsIdent(name.Value) {
		return nil
	}
	// Confirm the region opens here against the single decision table of
	// record before consuming anything, so the parser and the recognizer never
	// drift over what a func start site is.
	if RecognizeStartSite(kw.Value+" "+name.Value+"(").Site != StartFunc {
		return nil
	}

	txn := p.beginBashPPTxn()
	fd := &BashPPFuncDecl{Kw: kw, Name: name}
	sig := p.bashppSignature("func " + name.Value)
	if sig.nearMiss {
		txn.rollback(p)
		return nil
	}
	txn.commit(p)
	p.bashppRegisterFunc(name.Value)
	fd.Params, fd.Results = sig.params, sig.results
	fd.Lparen, fd.Rparen = sig.lparen, sig.rparen
	fd.ResLparen, fd.ResRparen = sig.resLparen, sig.resRparen
	fd.Body = p.bashppFuncBody("func "+name.Value, sig.rparen)
	return fd
}

// bashppMethodForm parses `func (r T) M(...)` and `func (r *T) M(...)`.
// The receiver opening is spaced from func, so it cannot collide with the
// classic shell definition `func() { ... }`.
func (p *Parser) bashppMethodForm(kw *Lit) Command {
	recv := &BashPPReceiver{Lparen: p.pos}
	p.next()
	nameWord := p.getWord()
	typeWord := p.getWord()
	recv.Name = bashppBareLit(nameWord)
	typ := bashppBareLit(typeWord)
	if recv.Name == nil || !bashppIsIdent(recv.Name.Value) || typ == nil {
		p.posErr(recv.Lparen, "method receiver must be one name and one named type")
		return nil
	}
	typeName := typ.Value
	if strings.HasPrefix(typeName, "*") {
		recv.Pointer = true
		typeName = strings.TrimPrefix(typeName, "*")
	}
	if !bashppIsIdent(typeName) || (recv.Pointer && strings.HasPrefix(typeName, "*")) {
		p.posErr(typ.Pos(), "invalid method receiver type")
		return nil
	}
	recv.RecvType = &Lit{ValuePos: posAddCol(typ.Pos(), len(typ.Value)-len(typeName)), ValueEnd: typ.End(), Value: typeName}
	if p.tok != rightParen {
		p.followErr(recv.Lparen, "func (receiver", rightParen)
	}
	recv.Rparen = p.pos
	p.next()
	methodWord := p.getWord()
	method := bashppBareLit(methodWord)
	if method == nil || !bashppIsIdent(method.Value) || p.tok != leftParen || p.spaced {
		p.posErr(recv.Rparen, "method declaration requires a name and signature")
		return nil
	}
	d := &BashPPFuncDecl{Kw: kw, Name: method, Receiver: recv}
	p.bashppRegisterFunc(method.Value)
	sig := p.bashppSignature("func (" + recv.Name.Value + " " + typ.Value + ") " + method.Value)
	d.Params, d.Results = sig.params, sig.results
	d.Lparen, d.Rparen = sig.lparen, sig.rparen
	d.ResLparen, d.ResRparen = sig.resLparen, sig.resRparen
	d.Body = p.bashppFuncBody("method "+method.Value, sig.rparen)
	return d
}

// bashppPointerMethodExpr parses the Class-R method expression
// `(*T).M(receiver, args...)` from a statement-opening parenthesis.
func (p *Parser) bashppPointerMethodExpr() Command {
	if p.tok != leftParen || p.r != '*' {
		return nil
	}
	txn := p.beginBashPPTxn()
	exprLparen := p.pos
	p.next()
	typeWord := p.getWord()
	typ := bashppBareLit(typeWord)
	if typ == nil || !strings.HasPrefix(typ.Value, "*") || !bashppIsIdent(strings.TrimPrefix(typ.Value, "*")) || p.tok != rightParen {
		txn.rollback(p)
		return nil
	}
	typeName := strings.TrimPrefix(typ.Value, "*")
	exprRparen := p.pos
	p.next()
	methodWord := p.getWord()
	method := bashppBareLit(methodWord)
	if method == nil || !strings.HasPrefix(method.Value, ".") || !bashppIsIdent(strings.TrimPrefix(method.Value, ".")) || p.tok != leftParen || p.spaced {
		txn.rollback(p)
		return nil
	}
	methodName := strings.TrimPrefix(method.Value, ".")
	callLparen := p.pos
	p.next()
	args, argNames, ellipsis, ok := p.bashppCallArgs()
	if !ok || p.tok != rightParen {
		txn.rollback(p)
		return nil
	}
	rparen := p.pos
	p.next()
	if !bashppCallTerminator(p.tok) {
		txn.rollback(p)
		return nil
	}
	txn.commit(p)
	typePos := posAddCol(typ.Pos(), 1)
	methodPos := posAddCol(method.Pos(), 1)
	return &BashPPCall{
		Fun: []*Lit{
			{ValuePos: typePos, ValueEnd: typ.End(), Value: typeName},
			{ValuePos: methodPos, ValueEnd: method.End(), Value: methodName},
		},
		Args: args, ArgNames: argNames, Ellipsis: ellipsis, PointerMethodExpr: true,
		Lparen: callLparen, Rparen: rparen, MethodExprLparen: exprLparen, MethodExprRparen: exprRparen,
		FuncLit: nil,
		// The outer expression parenthesis is represented by the boolean and
		// reconstructed by the printer; callLparen remains the argument list.
	}
}

// bashppRegisterFunc records name as a callable Bash++ function for the rest
// of the parse. It is what makes the ZERO-argument call `name()` recognizable:
// with arguments a call is unambiguous, but `name()` is also the prefix of a
// classic shell function definition, so only a name already known to be a
// Bash++ function may claim it — see [Parser.bashppParenForm]. Registering
// before the body is parsed is what lets a function recurse.
func (p *Parser) bashppRegisterFunc(name string) {
	if p.bashppFuncNames == nil {
		p.bashppFuncNames = make(map[string]bool)
	}
	p.bashppFuncNames[name] = true
}

// bashppPredeclaredFunc reports whether name is one of Go's predeclared
// functions that Bash++ implements. They are callable without a declaration,
// so they answer the zero-argument question [Parser.bashppRegisterFunc] answers
// for a declared name: `recover()` is a call, not the head of a shell function
// definition.
//
// The claim stays as narrow as the declared-name one. A body still rewinds the
// transaction — `recover() { …; }` is the shell function it has always been,
// because [bashppCallTerminator] admits only a statement end after the closing
// parenthesis — so what is claimed is exactly the shape bash rejects today.
func bashppPredeclaredFunc(name string) bool {
	switch name {
	case "panic", "recover":
		return true
	}
	return false
}

// bashppCallable reports whether a bare `name()` may be read as a Bash++ call:
// either a function declared earlier in this parse, or a predeclared one.
func (p *Parser) bashppCallable(name string) bool {
	return p.bashppFuncNames[name] || (p.lang.in(LangBashPP) && bashppPredeclaredFunc(name))
}

// bashppSig is a parsed Go-form signature: the parameter list, the optional
// results, and the positions of the parentheses that delimit them.
type bashppSig struct {
	params    []*BashPPField
	results   []*BashPPField
	lparen    Pos
	rparen    Pos
	resLparen Pos
	resRparen Pos
	nearMiss  bool
}

// bashppSignature parses `(params) results` with the parser sitting on the
// opening parenthesis. what names the construct for diagnostics, e.g.
// `func pick` or the bare `func` of a literal.
//
// The declaration and the literal share it deliberately: a literal's signature
// is the same grammar as a declaration's, and two copies would be two places
// for the variadic rules to drift apart.
func (p *Parser) bashppSignature(what string) bashppSig {
	sig := bashppSig{lparen: p.pos}
	p.next()
	sig.params, sig.nearMiss = p.bashppFieldList(sig.lparen, false)
	if p.tok != rightParen {
		p.followErr(sig.lparen, what+"(", rightParen)
	}
	sig.rparen = p.pos
	p.next()

	// Results are optional: none, a single bare type, or a parenthesised list.
	switch {
	case p.tok == leftParen:
		sig.resLparen = p.pos
		p.next()
		var nearMiss bool
		sig.results, nearMiss = p.bashppFieldList(sig.resLparen, true)
		sig.nearMiss = sig.nearMiss || nearMiss
		if p.tok != rightParen {
			p.followErr(sig.resLparen, what+"() (", rightParen)
		}
		sig.resRparen = p.pos
		p.next()
	case p.tok == _LitWord && !strings.HasPrefix(p.val, "{"):
		typ := p.lit(p.pos, p.val)
		if !bashppTypeName(typ.Value) {
			p.posErr(typ.Pos(), "func result must be a type name")
		}
		sig.results = []*BashPPField{{FieldType: typ}}
		p.next()
	}
	return sig
}

// bashppFuncBody parses the braced body of a declaration or a literal, with
// the func depth raised so that a `return` inside it is the Go-form one.
func (p *Parser) bashppFuncBody(what string, after Pos) *Block {
	p.got(_Newl)
	if p.tok == _LitWord && p.val == "{}" {
		pos := p.pos
		p.next()
		return &Block{Lbrace: pos, Rbrace: posAddCol(pos, 1)}
	}
	if !(p.tok == _LitWord && p.val == "{") {
		p.followErr(after, what+"()", noQuote("a { } body"))
	}
	p.bashppFuncDepth++
	var body Stmt
	p.block(&body)
	p.bashppFuncDepth--
	block, _ := body.Cmd.(*Block)
	return block
}

// bashppFuncLit parses a function literal with the parser sitting on the `(`
// that opens its parameter list, kw being the `func` already consumed.
//
// Like [Parser.bashppFuncForm] it commits forward rather than transactionally,
// and for the same reason: every site that reaches it — `f := func(`,
// `defer func(`, `return func(`, and the command-position `func(` with a
// parameter or a result — is a shape stock bash rejects outright, so there is
// no working script to protect and a malformed literal is a syntax error
// either way. What differs is only WHOSE diagnostic the user sees, and Go's is
// the more useful one here.
func (p *Parser) bashppFuncLit(kw *Lit) *BashPPFuncLit {
	lit := &BashPPFuncLit{Kw: kw}
	sig := p.bashppSignature("func")
	lit.Params, lit.Results = sig.params, sig.results
	lit.Lparen, lit.Rparen = sig.lparen, sig.rparen
	lit.ResLparen, lit.ResRparen = sig.resLparen, sig.resRparen
	lit.Body = p.bashppFuncBody("func", sig.rparen)
	return lit
}

// bashppFieldList reads a comma-separated Go-form parameter or result list up to
// the closing parenthesis. result selects how bare identifiers resolve: as
// unnamed result types when true, and as untyped parameter names when false.
func (p *Parser) bashppFieldList(open Pos, result bool) ([]*BashPPField, bool) {
	type segment struct {
		lits   []*Lit
		def    *Word
		equals Pos
	}
	var segs []segment
	var cur segment
	nearMiss := false
	for {
		p.got(_Newl)
		if p.tok == rightParen || p.tok == _EOF {
			break
		}
		w := p.getWord()
		if w == nil {
			p.followErr(open, "(", noQuote("a parameter"))
			break
		}
		clean, comma := bashppTrimComma(w)
		lit := bashppBareLit(clean)
		if lit == nil {
			p.posErr(w.Pos(), "func parameter must be a name or type")
		}
		if lit.Value == "==" {
			nearMiss = true
		}
		if !result && lit.Value == "=" {
			if cur.def != nil || len(cur.lits) == 0 {
				p.posErr(lit.Pos(), "malformed func parameter list")
			}
			cur.equals = lit.Pos()
			def := p.getWord()
			if def == nil {
				p.posErr(lit.Pos(), "default parameter requires a value")
			}
			def, comma = bashppTrimComma(def)
			if !bashppCallArg(def) {
				p.posErr(def.Pos(), "default parameter must be a value")
			}
			cur.def = def
		} else {
			cur.lits = append(cur.lits, lit)
		}
		if comma {
			segs = append(segs, cur)
			cur = segment{}
		}
	}
	if len(cur.lits) > 0 || cur.def != nil {
		segs = append(segs, cur)
	}
	var fields []*BashPPField
	var err error
	var plain [][]*Lit
	flush := func() {
		if err != nil || len(plain) == 0 {
			return
		}
		var resolved []*BashPPField
		resolved, err = bashppResolveFields(plain, result)
		fields = append(fields, resolved...)
		plain = nil
	}
	for _, seg := range segs {
		if seg.def == nil {
			plain = append(plain, seg.lits)
			continue
		}
		flush()
		if err != nil {
			break
		}
		if len(seg.lits) != 2 || !bashppIsIdent(seg.lits[0].Value) || !bashppTypeName(seg.lits[1].Value) {
			err = errBashppFieldList
			break
		}
		fields = append(fields, &BashPPField{
			Names: seg.lits[:1], FieldType: seg.lits[1],
			Default: seg.def, Equals: seg.equals,
		})
	}
	flush()
	switch {
	case err == nil:
	case err == errBashppFieldList:
		kind := "parameter"
		if result {
			kind = "result"
		}
		p.posErr(open, "malformed func %s list", kind)
	default:
		p.posErr(open, "%v", err)
	}
	return fields, nearMiss
}

// errBashppFieldList is the generic "this is not a signature we spell" verdict,
// reported as a malformed list. A field-list rule with a Go diagnostic of its
// own — the only-final rule for `...` — returns that message instead, because
// "malformed parameter list" would send a reader looking for a typo that is
// not there.
var errBashppFieldList = errors.New("malformed func field list")

// errBashppEllipsisFinal is Go's own wording for a variadic group that is not
// last, which is the mistake a reader is most likely to make once `...` exists.
var errBashppEllipsisFinal = errors.New("can only use ... with final parameter")

// bashppResolveFields turns the comma-separated word groups of a signature into
// [BashPPField]s, applying Go's rule that a trailing type distributes back over
// the names that precede it, plus the Bash++ extension that a run of bare
// parameter names with no type at all is an untyped group.
func bashppResolveFields(segs [][]*Lit, result bool) ([]*BashPPField, error) {
	var fields []*BashPPField
	var pending []*Lit
	sawType := false
	sawVariadic := false
	for _, seg := range segs {
		// A `...T` group ends the list. Go allows the variadic parameter only
		// last, and allows it only ONE name — `func f(a, b ...int)` shares the
		// type across both names and is rejected by the gc front end with the
		// message reused here — so both rules are checked where the group is
		// built rather than left to a later pass that would have to
		// reconstruct which group carried the dots.
		if last := seg[len(seg)-1]; strings.HasPrefix(last.Value, "...") {
			if result || sawVariadic || len(seg) > 2 || len(pending) > 0 {
				return nil, errBashppEllipsisFinal
			}
			elem, ok := bashppEllipsisElem(last)
			if !ok {
				return nil, errBashppFieldList
			}
			field := &BashPPField{FieldType: elem, Ellipsis: last.Pos()}
			if len(seg) == 2 {
				if !bashppIsIdent(seg[0].Value) {
					return nil, errBashppFieldList
				}
				field.Names = []*Lit{seg[0]}
			}
			fields = append(fields, field)
			sawType, sawVariadic = true, true
			continue
		}
		if sawVariadic {
			return nil, errBashppEllipsisFinal
		}
		switch len(seg) {
		case 1:
			pending = append(pending, seg[0])
		case 2:
			name, typ := seg[0], seg[1]
			if !bashppTypeName(typ.Value) {
				return nil, errBashppFieldList
			}
			names := append(pending, name)
			for _, n := range names {
				if !bashppIsIdent(n.Value) {
					return nil, errBashppFieldList
				}
			}
			fields = append(fields, &BashPPField{Names: names, FieldType: typ})
			pending = nil
			sawType = true
		default:
			return nil, errBashppFieldList
		}
	}
	if len(pending) == 0 {
		return fields, nil
	}
	// Trailing bare identifiers. After a typed group they are a mixed
	// named/unnamed list, which Go rejects; with no type anywhere they are
	// either unnamed result types or untyped parameter names.
	if sawType {
		return nil, errBashppFieldList
	}
	if result {
		for _, t := range pending {
			if !bashppTypeName(t.Value) {
				return nil, errBashppFieldList
			}
			fields = append(fields, &BashPPField{FieldType: t})
		}
		return fields, nil
	}
	for _, n := range pending {
		if !bashppIsIdent(n.Value) {
			return nil, errBashppFieldList
		}
	}
	return append(fields, &BashPPField{Names: pending}), nil
}

// bashppEllipsisElem splits a `...T` word into the element type T, positioned
// where T was written so the printer and every diagnostic point at the type
// rather than at the dots.
func bashppEllipsisElem(lit *Lit) (*Lit, bool) {
	name := strings.TrimPrefix(lit.Value, "...")
	if !bashppTypeName(name) {
		return nil, false
	}
	pos := posAddCol(lit.ValuePos, 3)
	return &Lit{ValuePos: pos, ValueEnd: posAddCol(pos, len(name)), Value: name}, true
}

// bashppTypeName reports whether s is a supported type spelling: a bare
// identifier, dotted selector (`time.Duration`), or the pointer to a named
// type needed by P3-C. Other composite types remain a later tranche.
func bashppTypeName(s string) bool {
	// `func` is the one reserved word admitted as a type spelling, and it is
	// the WHOLE spelling: P3-B gives a function value the bare type `func`
	// rather than Go's full `func(int) error`, whose parentheses and commas
	// would have to survive the shell's word splitting inside a signature that
	// is itself parenthesised. A parameter or result typed `func` holds a
	// closure; the argument and result types it accepts are unchecked, which
	// is the same latitude every other type spelling gets in this phase.
	if s == "func" {
		return true
	}
	if strings.HasPrefix(s, "*") {
		return bashppIsIdent(strings.TrimPrefix(s, "*"))
	}
	if !bashppSelector(s) {
		return false
	}
	head, _, _ := strings.Cut(s, ".")
	return head != "" && !isGoReservedWord(head)
}

// bashppDeferForm parses `defer f(x)` once the call parser has collected
// `defer f` and stopped at `(`. The call itself is recognized by the shared
// paren-form machinery, so an unsupported argument rewinds to the shell exactly
// as a bare call would, keeping `defer cleanup` and other Class E shapes shell.
//
// `defer func(…) { … }()` — a deferred closure — is the same site with a
// literal in callee position. It cannot rewind, because the literal commits
// forward like every other one; that costs nothing, since the parenthesis
// after `defer func` is a bash syntax error whatever follows it.
func (p *Parser) bashppDeferForm(ce *CallExpr) Command {
	if ce == nil || len(ce.Assigns) != 0 || len(ce.Args) != 2 || p.tok != leftParen || p.spaced {
		return nil
	}
	kw := bashppBareLit(ce.Args[0])
	name := bashppBareLit(ce.Args[1])
	if kw == nil || kw.Value != "defer" || name == nil {
		return nil
	}
	// The peek byte is part of the probe because `defer func()` and
	// `defer func(n int)` differ only past the parenthesis, and the table
	// answers about the literal from the byte that follows it.
	if RecognizeStartSite(kw.Value+" "+name.Value+"("+p.bashppPeekByte()).Site != StartDefer {
		return nil
	}
	if name.Value == "func" {
		call := p.bashppLitCall(p.bashppFuncLit(name))
		if call == nil {
			return nil
		}
		return &BashPPDefer{Kw: kw, Call: call}
	}
	if !bashppSelector(name.Value) {
		return nil
	}
	cmd := p.bashppParenForm(&CallExpr{Args: []*Word{ce.Args[1]}})
	call, ok := cmd.(*BashPPCall)
	if !ok {
		return nil
	}
	return &BashPPDefer{Kw: kw, Call: call}
}

// bashppFuncLitForm claims the sites at which a function literal may stand,
// with the parser sitting on the `(` that opens the literal's parameter list
// and ce holding the words already collected:
//
//	func(n int) { … }(1)            an immediately invoked literal
//	greet := func(who string) { … } a literal bound to a name
//	n := func() int { … }()         a literal invoked into a binding
//	return func(n int) int { … }    a literal escaping its factory
//
// `defer func() { … }()` is the fifth, and belongs to [Parser.bashppDeferForm]
// because `defer` reaches the dispatch first.
//
// It returns nil for every other shape, including the bare `func(` at a
// command position with an empty parameter list, which stays the bash function
// definition it is today — see [recognizeFuncLit].
func (p *Parser) bashppFuncLitForm(ce *CallExpr) Command {
	kw := bashppBareLit(ce.Args[len(ce.Args)-1])
	if kw == nil || kw.Value != "func" {
		return nil
	}
	switch {
	case len(ce.Args) == 1:
		// A command position. Only the recognizer decides whether the region
		// opens here, because this is the one literal site that competes with
		// a shape bash accepts.
		if RecognizeStartSite("func("+p.bashppPeekByte()).Site != StartFuncLit {
			return nil
		}
		call := p.bashppLitCall(p.bashppFuncLit(kw))
		if call == nil {
			return nil
		}
		return call
	case len(ce.Args) == 2 && bashppLitValue(ce.Args[0]) == "return":
		// `return func(…) { … }`, which is how a closure escapes the function
		// that built it. It is claimed only inside a committed func body: a
		// `return` outside one is the shell builtin, which is Class E, and
		// nothing about it may change.
		if p.bashppFuncDepth == 0 {
			return nil
		}
		ret := bashppBareLit(ce.Args[0])
		return &BashPPReturn{Kw: ret, FuncLit: p.bashppFuncLit(kw)}
	case len(ce.Args) >= 3 && bashppLitValue(ce.Args[len(ce.Args)-2]) == ":=":
		lhs, ok := bashppShortLHS(ce.Args[:len(ce.Args)-2])
		if !ok {
			return nil
		}
		opPos := ce.Args[len(ce.Args)-2].Pos()
		lit := p.bashppFuncLit(kw)
		if p.tok == leftParen && !p.spaced {
			// `n := func() int { … }()` binds the RESULT of the call, so the
			// node carries the invocation and not the function.
			call := p.bashppLitCall(lit)
			if call == nil {
				return nil
			}
			return &BashPPShortDecl{Lhs: lhs, Class: ClassR, OpPos: opPos, Call: call}
		}
		// A name bound to a literal is callable with no arguments from here
		// on, which is the whole point of binding it; see
		// [Parser.bashppRegisterFunc] for why that has to be recorded.
		for _, name := range lhs {
			p.bashppRegisterFunc(name.Value)
		}
		return &BashPPShortDecl{Lhs: lhs, Class: ClassR, OpPos: opPos, FuncLit: lit}
	}
	return nil
}

// bashppLitValue is the bare literal text of w, or "" when w is not one.
func bashppLitValue(w *Word) string {
	if lit := bashppBareLit(w); lit != nil {
		return lit.Value
	}
	return ""
}

// bashppPeekByte is the one byte past the parser's current token, as a string,
// or "" at end of input. It exists so a recognizer probe can be assembled from
// the same lookahead the streaming lexer already holds, without reading ahead.
func (p *Parser) bashppPeekByte() string {
	if p.r >= utf8.RuneSelf {
		return ""
	}
	return string(p.r)
}

// bashppLitCall parses the `(args)` that immediately invokes a literal.
//
// A literal at these sites must be called: Go has no bare-literal statement
// either, and the sites that do NOT require an invocation — `greet := func…`,
// `return func…` — never reach here.
func (p *Parser) bashppLitCall(lit *BashPPFuncLit) *BashPPCall {
	if p.tok != leftParen {
		p.posErr(lit.Pos(), "func literal must be called or bound to a name")
		return nil
	}
	lparen := p.pos
	p.next()
	args, argNames, ellipsis, ok := p.bashppCallArgs()
	if !ok || p.tok != rightParen {
		p.posErr(lparen, "malformed func literal argument list")
		return nil
	}
	rparen := p.pos
	p.next()
	return &BashPPCall{
		FuncLit: lit, Args: args, ArgNames: argNames, Ellipsis: ellipsis,
		Lparen: lparen, Rparen: rparen,
	}
}

// bashppReturn reclassifies a completed `return …` command as a Go-form return,
// but only inside a Bash++ func body. Outside one it returns nil so the command
// stays the ordinary shell builtin, which is Class E and must keep its meaning.
func (p *Parser) bashppReturn(ce *CallExpr, redirs []*Redirect) *BashPPReturn {
	if p.bashppFuncDepth == 0 || ce == nil || len(ce.Assigns) != 0 || len(redirs) != 0 || len(ce.Args) == 0 {
		return nil
	}
	kw := bashppBareLit(ce.Args[0])
	if kw == nil || kw.Value != "return" {
		return nil
	}
	if len(ce.Args) == 1 {
		return &BashPPReturn{Kw: kw}
	}
	vals := bashppReturnValues(ce.Args[1:])
	if vals == nil {
		// A comma placement the tuple grammar does not spell — leave it to the
		// shell `return` builtin rather than guess at the intent.
		return nil
	}
	return &BashPPReturn{Kw: kw, Results: vals}
}

// bashppReturnValues splits a return operand list into one word per value.
// Inside a committed func body the compatibility grammar no longer applies —
// bash never reaches this code — so any word may be returned; only the tuple's
// comma placement is checked, so `return a, b` yields two values while a stray
// comma falls back to the shell builtin.
func bashppReturnValues(words []*Word) []*Word {
	out := make([]*Word, 0, len(words))
	for i, w := range words {
		clean, comma := bashppTrimComma(w)
		if comma != (i < len(words)-1) {
			return nil
		}
		out = append(out, clean)
	}
	return out
}
