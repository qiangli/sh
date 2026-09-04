// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import "strings"

// bashppSwitch parses the enum switch surface after a typed function body has
// already committed us to Bash# grammar. A top-level `switch` remains an
// ordinary shell command, preserving the Class-E near-miss boundary.
func (p *Parser) bashppSwitch(stmt *Stmt) {
	sw := &BashPPSwitch{Switch: p.pos}
	p.next()
	sw.Expr = p.getWord()
	if sw.Expr == nil || bashppBareLit(sw.Expr) == nil {
		p.followErr(sw.Switch, "switch", noQuote("an enum expression"))
		return
	}
	if !(p.tok == _LitWord && p.val == "{") {
		p.followErr(sw.Expr.End(), "switch expression", noQuote("a { } body"))
		return
	}
	sw.Lbrace = p.pos
	p.next()

	for p.err == nil {
		p.got(_Newl)
		if p.tok == _EOF {
			p.matchingErr(sw.Lbrace, leftBrace, rightBrace)
			break
		}
		if p.tok == _LitWord && p.val == "}" {
			sw.Rbrace = p.pos
			p.next()
			break
		}
		arm := &BashPPSwitchArm{Case: p.pos}
		if p.tok == _LitWord && p.val == "default:" {
			arm.Colon = posAddCol(p.pos, len("default"))
			p.next()
		} else {
			if !(p.tok == _LitWord && p.val == "case") {
				p.curErr("switch body requires case or default arms")
				break
			}
			p.next()
			word := p.getWord()
			lit := bashppBareLit(word)
			if lit == nil || !strings.HasSuffix(lit.Value, ":") || len(lit.Value) == 1 {
				p.followErr(arm.Case, "case", noQuote("an enum member followed by :"))
				break
			}
			value := strings.TrimSuffix(lit.Value, ":")
			arm.Member = &Lit{ValuePos: lit.ValuePos, ValueEnd: posAddCol(lit.ValueEnd, -1), Value: value}
			arm.Colon = posAddCol(lit.ValueEnd, -1)
		}
		arm.Stmts, arm.Last = p.stmtList("case", "default:", "}")
		sw.Arms = append(sw.Arms, arm)
	}
	stmt.Cmd = sw
}
