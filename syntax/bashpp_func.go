// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import "strings"

// Parsing of the Bash++ P3-A ("typed functions") command-position sites:
// `func name(…) … { … }`, `defer f(x)` and the func-body `return`.
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
	if ce == nil || len(ce.Assigns) != 0 || len(ce.Args) != 2 || p.tok != leftParen {
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

	fd := &BashPPFuncDecl{Kw: kw, Name: name, Lparen: p.pos}
	if p.bashppFuncNames == nil {
		p.bashppFuncNames = make(map[string]bool)
	}
	// Register before parsing the body so recursion and zero-argument calls in
	// that body are recognized while the declaration is still being read.
	p.bashppFuncNames[name.Value] = true
	p.next()
	fd.Params = p.bashppFieldList(fd.Lparen, false)
	if p.tok != rightParen {
		p.followErr(fd.Lparen, "func "+name.Value+"(", rightParen)
	}
	fd.Rparen = p.pos
	p.next()

	// Results are optional: none, a single bare type, or a parenthesised list.
	switch {
	case p.tok == leftParen:
		fd.ResLparen = p.pos
		p.next()
		fd.Results = p.bashppFieldList(fd.ResLparen, true)
		if p.tok != rightParen {
			p.followErr(fd.ResLparen, "func "+name.Value+"() (", rightParen)
		}
		fd.ResRparen = p.pos
		p.next()
	case p.tok == _LitWord && p.val != "{":
		typ := p.lit(p.pos, p.val)
		if !bashppTypeName(typ.Value) {
			p.posErr(typ.Pos(), "func result must be a type name")
		}
		fd.Results = []*BashPPField{{FieldType: typ}}
		p.next()
	}

	p.got(_Newl)
	if !(p.tok == _LitWord && p.val == "{") {
		p.followErr(fd.Rparen, "func "+name.Value+"()", noQuote("a { } body"))
	}
	p.bashppFuncDepth++
	var body Stmt
	p.block(&body)
	p.bashppFuncDepth--
	fd.Body, _ = body.Cmd.(*Block)
	return fd
}

// bashppFieldList reads a comma-separated Go-form parameter or result list up to
// the closing parenthesis. result selects how bare identifiers resolve: as
// unnamed result types when true, and as untyped parameter names when false.
func (p *Parser) bashppFieldList(open Pos, result bool) []*BashPPField {
	var segs [][]*Lit
	var cur []*Lit
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
		cur = append(cur, lit)
		if comma {
			segs = append(segs, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		segs = append(segs, cur)
	}
	fields, ok := bashppResolveFields(segs, result)
	if !ok {
		kind := "parameter"
		if result {
			kind = "result"
		}
		p.posErr(open, "malformed func %s list", kind)
	}
	return fields
}

// bashppResolveFields turns the comma-separated word groups of a signature into
// [BashPPField]s, applying Go's rule that a trailing type distributes back over
// the names that precede it, plus the Bash++ extension that a run of bare
// parameter names with no type at all is an untyped group.
func bashppResolveFields(segs [][]*Lit, result bool) ([]*BashPPField, bool) {
	var fields []*BashPPField
	var pending []*Lit
	sawType := false
	for _, seg := range segs {
		switch len(seg) {
		case 1:
			pending = append(pending, seg[0])
		case 2:
			name, typ := seg[0], seg[1]
			if !bashppTypeName(typ.Value) {
				return nil, false
			}
			names := append(pending, name)
			for _, n := range names {
				if !bashppIsIdent(n.Value) {
					return nil, false
				}
			}
			fields = append(fields, &BashPPField{Names: names, FieldType: typ})
			pending = nil
			sawType = true
		default:
			return nil, false
		}
	}
	if len(pending) == 0 {
		return fields, true
	}
	// Trailing bare identifiers. After a typed group they are a mixed
	// named/unnamed list, which Go rejects; with no type anywhere they are
	// either unnamed result types or untyped parameter names.
	if sawType {
		return nil, false
	}
	if result {
		for _, t := range pending {
			if !bashppTypeName(t.Value) {
				return nil, false
			}
			fields = append(fields, &BashPPField{FieldType: t})
		}
		return fields, true
	}
	for _, n := range pending {
		if !bashppIsIdent(n.Value) {
			return nil, false
		}
	}
	return append(fields, &BashPPField{Names: pending}), true
}

// bashppTypeName reports whether s is a supported type spelling: a bare
// identifier or a dotted selector (`time.Duration`). Composite types — slices,
// maps, pointers — are deliberately excluded from P3-A; their spellings tangle
// with the shell's glob and brace tokens and are left to a later tranche.
func bashppTypeName(s string) bool {
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
func (p *Parser) bashppDeferForm(ce *CallExpr) Command {
	if ce == nil || len(ce.Assigns) != 0 || len(ce.Args) != 2 || p.tok != leftParen || p.spaced {
		return nil
	}
	kw := bashppBareLit(ce.Args[0])
	name := bashppBareLit(ce.Args[1])
	if kw == nil || kw.Value != "defer" || name == nil || !bashppSelector(name.Value) {
		return nil
	}
	if RecognizeStartSite(kw.Value+" "+name.Value+"(").Site != StartDefer {
		return nil
	}
	cmd := p.bashppParenForm(&CallExpr{Args: []*Word{ce.Args[1]}})
	call, ok := cmd.(*BashPPCall)
	if !ok {
		return nil
	}
	return &BashPPDefer{Kw: kw, Call: call}
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
