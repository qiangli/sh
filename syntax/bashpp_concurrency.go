// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

// bashppSelect recognizes the Class-R brace form.  It deliberately lives in
// syntax: this commits a Go region for parsing only; it does not imply any
// scheduling or channel runtime behaviour.
//
// The whole body is read inside a transaction because `select` is ALSO a shell
// keyword — `select x in a b; do …; done` — and a script inside a Bash++ func
// body is still entitled to it. Every shape the Go grammar does not spell is
// rewound byte for byte and handed to [Parser.selectClause], so the shell form
// is decided by what it is rather than by which recognizer ran first.
func (p *Parser) bashppSelect(stmt *Stmt) bool {
	txn := p.beginBashPPTxn()
	sel := &BashPPSelect{Select: p.pos}
	p.next()
	p.got(_Newl)
	if p.tok == _LitWord && p.val == "{}" {
		sel.Lbrace = p.pos
		sel.Rbrace = posAddCol(p.pos, 1)
		p.next()
		txn.commit(p)
		stmt.Cmd = sel
		return true
	}
	if !(p.tok == _LitWord && p.val == "{") {
		txn.rollback(p)
		return false
	}
	// An empty select is useful on its own and, unlike the shell `select`
	// builtin, is unambiguously the Go brace production.
	sel.Lbrace = p.pos
	p.next()
	for {
		p.got(_Newl)
		if p.err != nil || p.tok == _EOF {
			// A `select x in …` whose word list happened to open with `{`
			// lands here. Rewinding is the only correct answer: the shell
			// form must not inherit a diagnostic phrased in terms of Go.
			txn.rollback(p)
			return false
		}
		if p.tok == _LitWord && p.val == "}" {
			sel.Rbrace = p.pos
			p.next()
			break
		}
		arm := p.bashppSelectArm()
		if arm == nil || p.err != nil {
			txn.rollback(p)
			return false
		}
		sel.Cases = append(sel.Cases, arm)
	}
	txn.commit(p)
	stmt.Cmd = sel
	return true
}

// bashppSelectArm parses one `case <comm>:` or `default:` arm and its
// statements. It returns nil for anything else, which rewinds the whole select.
func (p *Parser) bashppSelectArm() *BashPPSelectCase {
	arm := &BashPPSelectCase{Case: p.pos}
	arm.Comments, p.accComs = p.accComs, nil
	switch {
	case p.tok == _LitWord && p.val == "default:":
		arm.Default = true
		arm.Colon = posAddCol(p.pos, len("default"))
		p.next()
	case p.tok == _LitWord && p.val == "case":
		p.next()
		comm, colon := p.bashppCommClause()
		if comm == nil {
			return nil
		}
		arm.Comm, arm.Colon = comm, colon
	default:
		return nil
	}
	// A comment lexed while advancing beyond the colon belongs to the arm
	// header only when it shares the colon's source line. Later comments stay
	// accumulated for the first body statement.
	for len(p.accComs) > 0 && p.accComs[0].Pos().Line() == arm.Colon.Line() {
		arm.Comments = append(arm.Comments, p.accComs[0])
		p.accComs = p.accComs[1:]
	}
	// The same stop set the enum switch uses, so an arm body ends where the
	// next arm or the closing brace begins.
	arm.Stmts, arm.Last = p.stmtList("case", "default:", "}")
	return arm
}

// bashppCommClause reads a select case's communication clause up to and
// including its terminating colon, and returns the typed node for it.
//
// It reads at the TOKEN level rather than reusing the statement parser because
// the colon is glued to the last word — `case <-done:` ends in the single
// literal `-done:` — so there is no statement boundary for the ordinary
// machinery to stop at. What it does NOT do is decide the shape: the words it
// collects are handed to [bashppClassifyComm], the same classifier the
// statement-level forms use, so `case v := <-ch:` and a bare `v := <-ch` are
// one grammar rather than two that must be kept in agreement by hand.
func (p *Parser) bashppCommClause() (Command, Pos) {
	var pre, post []*Word
	var arrow, colon Pos
	for {
		if p.tok == rdrIn {
			if arrow.IsValid() {
				return nil, Pos{}
			}
			arrow = p.pos
			p.next()
			continue
		}
		if p.tok == _Newl || p.tok == _EOF || p.tok == semicolon {
			return nil, Pos{}
		}
		w := p.getWord()
		if w == nil {
			return nil, Pos{}
		}
		trimmed, colonPos, done := bashppTrimColon(w)
		if !done {
			if arrow.IsValid() {
				post = append(post, w)
			} else {
				pre = append(pre, w)
			}
			continue
		}
		colon = colonPos
		if trimmed != nil {
			if arrow.IsValid() {
				post = append(post, trimmed)
			} else {
				pre = append(pre, trimmed)
			}
		}
		break
	}
	if !arrow.IsValid() {
		return nil, Pos{}
	}
	cmd := bashppClassifyComm(pre, arrow, post)
	if cmd == nil {
		return nil, Pos{}
	}
	return cmd, colon
}

// bashppTrimColon splits a terminating `:` off a word, mirroring
// [bashppTrimComma]. The trimmed word is nil when the colon was the whole of
// it, which is the `case <-ch :` spelling.
func bashppTrimColon(w *Word) (*Word, Pos, bool) {
	if w == nil || len(w.Parts) == 0 {
		return w, Pos{}, false
	}
	last, ok := w.Parts[len(w.Parts)-1].(*Lit)
	if !ok || len(last.Value) == 0 || last.Value[len(last.Value)-1] != ':' {
		return w, Pos{}, false
	}
	colon := posAddCol(last.ValueEnd, -1)
	copyWord := *w
	copyWord.Parts = append([]WordPart(nil), w.Parts...)
	copyLit := *last
	copyLit.Value = last.Value[:len(last.Value)-1]
	copyLit.ValueEnd = colon
	if copyLit.Value == "" {
		copyWord.Parts = copyWord.Parts[:len(copyWord.Parts)-1]
	} else {
		copyWord.Parts[len(copyWord.Parts)-1] = &copyLit
	}
	if len(copyWord.Parts) == 0 {
		return nil, colon, true
	}
	return &copyWord, colon, true
}

// bashppRange recognizes range-over-channel, `for v := range ch { … }`.
//
// The shell `for` is Class E and heavily used, so the whole clause is read
// inside a transaction and every shape that is not exactly the Go one is
// rewound to [Parser.forClause]. The commit point is the `range` keyword: bash
// has no `for name := range` production — `for foo` must be followed by `in`,
// `do`, `;` or a newline — so by the time the third word is read the shape is
// already a bash syntax error and claiming it takes nothing away.
func (p *Parser) bashppRange(stmt *Stmt) bool {
	txn := p.beginBashPPTxn()
	rng := &BashPPRange{For: p.pos}
	p.next()
	// Exactly one iteration variable, or none. Go admits a second value only
	// for maps, slices and strings; a channel yields one element per receive,
	// so `for v, ok := range ch` does not compile there and is not spelled
	// here either — accepting it would build a tree no interpreter can run.
	if !(p.tok == _LitWord && p.val == "range") {
		if p.tok != _LitWord {
			txn.rollback(p)
			return false
		}
		lit := bashppBareLit(p.getWord())
		if lit == nil || !bashppIsIdent(lit.Value) {
			txn.rollback(p)
			return false
		}
		rng.Names = append(rng.Names, lit)
		op := bashppBareLit(p.getWord())
		if op == nil || op.Value != ":=" {
			txn.rollback(p)
			return false
		}
		rng.Define = op.Pos()
	}
	if !(p.tok == _LitWord && p.val == "range") {
		txn.rollback(p)
		return false
	}
	rng.Range = p.pos
	p.next()
	ch := p.getWord()
	if ch == nil || !bashppChanOperand(ch) {
		txn.rollback(p)
		return false
	}
	rng.Chan = ch
	p.got(_Newl)
	if !(p.tok == _LitWord && p.val == "{") {
		txn.rollback(p)
		return false
	}
	var body Stmt
	p.block(&body)
	block, _ := body.Cmd.(*Block)
	if block == nil || p.err != nil {
		txn.rollback(p)
		return false
	}
	rng.Body = block
	txn.commit(p)
	stmt.Cmd = rng
	return true
}
