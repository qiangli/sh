// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

// bashppSelect recognizes the Class-R brace form.  It deliberately lives in
// syntax: this commits a Go region for parsing only; it does not imply any
// scheduling or channel runtime behaviour.
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
	// builtin, is unambiguously the Go brace production. More elaborate case
	// bodies are parsed by the normal committed-region statement machinery as
	// that grammar is extended.
	sel.Lbrace = p.pos
	p.next()
	p.got(_Newl)
	if !(p.tok == _LitWord && p.val == "}") {
		txn.rollback(p)
		return false
	}
	sel.Rbrace = p.pos
	p.next()
	txn.commit(p)
	stmt.Cmd = sel
	return true
}
