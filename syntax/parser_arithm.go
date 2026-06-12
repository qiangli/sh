package syntax

import "strings"

// compact specifies whether we allow spaces between expressions.
// This is true for let
func (p *Parser) arithmExpr(compact bool) ArithmExpr {
	return p.arithmExprComma(compact)
}

// These function names are inspired by Bash's expr.c

func (p *Parser) arithmExprComma(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprAssign, Comma)
}

func (p *Parser) arithmExprAssign(compact bool) ArithmExpr {
	// Assign is different from the other binary operators because it's
	// right-associative and needs to check that it's placed after a name
	value := p.arithmExprTernary(compact)
	switch BinAritOperator(p.tok) {
	case AddAssgn, SubAssgn, MulAssgn, QuoAssgn, RemAssgn, AndAssgn,
		OrAssgn, XorAssgn, ShlAssgn, ShrAssgn, Assgn,
		AndBoolAssgn, OrBoolAssgn, XorBoolAssgn, PowAssgn:
		if compact && p.spaced {
			return value
		}
		// Bash 5.3 accepts assignment to non-name lvalue at parse
		// time (`7=4`, `(a)=4`) and errors at runtime with
		// "attempted assignment to non-variable". The runtime
		// check is in expand/arith.go. Without this, for-loop
		// expressions that the bash 5.3 suite deliberately feeds
		// bad lvalues to abort the whole surrounding parse.
		pos := p.pos
		tok := p.tok
		p.nextArithOp(compact)
		y := p.arithmExprAssign(compact)
		if y == nil {
			if p.quote == arithmExprCmd && (p.tok == semicolon || p.tok == dblSemicolon || p.tok == rightParen) {
				y = p.wordOne(&Lit{ValuePos: p.pos, ValueEnd: p.pos})
			} else {
				p.followErrExp(pos, tok)
			}
		}
		return &BinaryArithm{
			OpPos: pos,
			Op:    BinAritOperator(tok),
			X:     value,
			Y:     y,
		}
	}
	return value
}

func (p *Parser) arithmExprTernary(compact bool) ArithmExpr {
	value := p.arithmExprLor(compact)
	if BinAritOperator(p.tok) != TernQuest || (compact && p.spaced) {
		return value
	}

	if value == nil {
		p.curErr("%#q must follow an expression", p.tok)
	}
	questPos := p.pos
	p.nextArithOp(compact)
	missingExpr := func() ArithmExpr {
		return p.wordOne(&Lit{ValuePos: p.pos, ValueEnd: p.pos})
	}
	if BinAritOperator(p.tok) == TernColon {
		trueExpr := missingExpr()
		colonPos := p.pos
		p.nextArithOp(compact)
		falseExpr := p.arithmExprTernary(compact)
		if falseExpr == nil {
			falseExpr = missingExpr()
		}
		return &BinaryArithm{
			OpPos: questPos,
			Op:    TernQuest,
			X:     value,
			Y: &BinaryArithm{
				OpPos: colonPos,
				Op:    TernColon,
				X:     trueExpr,
				Y:     falseExpr,
			},
		}
	}
	trueExpr := p.arithmExpr(compact)
	if trueExpr == nil {
		trueExpr = missingExpr()
	}
	if BinAritOperator(p.tok) != TernColon {
		return &BinaryArithm{
			OpPos: questPos,
			Op:    TernQuest,
			X:     value,
			Y: &BinaryArithm{
				OpPos: questPos,
				Op:    TernColon,
				X:     trueExpr,
				Y:     missingExpr(),
			},
		}
	}
	colonPos := p.pos
	p.nextArithOp(compact)
	// Bash gives assignment lower precedence than the conditional
	// operator in the false branch: `1 ? 20 : x+=2` is parsed like
	// `(1 ? 20 : x) += 2`, so it errors as assignment to a non-variable
	// even though the false branch is not evaluated. Parentheses still
	// allow an assignment expression there: `1 ? 20 : (x+=2)`.
	falseExpr := p.arithmExprTernary(compact)
	if falseExpr == nil {
		falseExpr = missingExpr()
	}
	return &BinaryArithm{
		OpPos: questPos,
		Op:    TernQuest,
		X:     value,
		Y: &BinaryArithm{
			OpPos: colonPos,
			Op:    TernColon,
			X:     trueExpr,
			Y:     falseExpr,
		},
	}
}

func (p *Parser) arithmExprLor(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprLand, OrArit, XorBool)
}

func (p *Parser) arithmExprLand(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprBor, AndArit)
}

func (p *Parser) arithmExprBor(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprBxor, Or)
}

func (p *Parser) arithmExprBxor(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprBand, Xor)
}

func (p *Parser) arithmExprBand(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprEquality, And)
}

func (p *Parser) arithmExprEquality(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprComparison, Eql, Neq)
}

func (p *Parser) arithmExprComparison(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprShift, Lss, Gtr, Leq, Geq)
}

func (p *Parser) arithmExprShift(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprAddition, Shl, Shr)
}

func (p *Parser) arithmExprAddition(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprMultiplication, Add, Sub)
}

func (p *Parser) arithmExprMultiplication(compact bool) ArithmExpr {
	return p.arithmExprBinary(compact, p.arithmExprPower, Mul, Quo, Rem)
}

func (p *Parser) arithmExprPower(compact bool) ArithmExpr {
	// Power is different from the other binary operators because it's right-associative
	value := p.arithmExprUnary(compact)
	if BinAritOperator(p.tok) != Pow || (compact && p.spaced) {
		return value
	}

	if value == nil {
		p.curErr("%#q must follow an expression", p.tok)
	}

	op := p.tok
	pos := p.pos
	p.nextArithOp(compact)
	y := p.arithmExprPower(compact)
	if y == nil {
		p.followErrExp(pos, op)
	}
	return &BinaryArithm{
		OpPos: pos,
		Op:    BinAritOperator(op),
		X:     value,
		Y:     y,
	}
}

func (p *Parser) arithmExprUnary(compact bool) ArithmExpr {
	if !compact {
		p.got(_Newl)
	}

	switch UnAritOperator(p.tok) {
	case Not, BitNegation, Plus, Minus:
		ue := &UnaryArithm{OpPos: p.pos, Op: UnAritOperator(p.tok)}
		p.nextArithOp(compact)
		if ue.X = p.arithmExprUnary(compact); ue.X == nil {
			p.followErrExp(ue.OpPos, ue.Op)
		}
		return ue
	}
	return p.arithmExprValue(compact)
}

func (p *Parser) arithmExprValue(compact bool) ArithmExpr {
	var x ArithmExpr
	switch p.tok {
	case addAdd, subSub:
		ue := &UnaryArithm{OpPos: p.pos, Op: UnAritOperator(p.tok)}
		p.nextArith(compact)
		switch p.tok {
		case semicolon:
			// Only a C-style for loop (`for (( -- ; ++; -- ))`)
			// legitimately ends an arithmetic expression at `;`;
			// bash accepts the parse and errors at runtime.
			if p.quote != arithmExprCmd {
				p.followErr(ue.OpPos, ue.Op, noQuote("a literal"))
			}
			ue.X = p.wordOne(&Lit{ValuePos: p.pos, ValueEnd: p.pos})
		case rightParen, dblRightParen, _EOF:
			ue.X = p.wordOne(&Lit{ValuePos: p.pos, ValueEnd: p.pos})
		case plus, minus:
			ue.X = p.arithmExprUnary(compact)
		default:
			if p.tok != _LitWord {
				p.followErr(ue.OpPos, ue.Op, noQuote("a literal"))
			}
			ue.X = p.arithmExprValue(compact)
		}
		return ue
	case leftParen:
		if p.quote == paramExpArithm && p.lang.in(LangZsh) {
			x = p.zshSubFlags()
			break
		}
		pe := &ParenArithm{Lparen: p.pos}
		p.nextArithOp(compact)
		pe.X = p.followArithm(leftParen, pe.Lparen)
		pe.Rparen = p.matched(pe.Lparen, leftParen, rightParen)
		if p.quote == paramExpArithm && p.tok == _LitWord {
			p.checkLang(pe.Lparen, LangZsh, "subscript flags")
		}
		x = pe
	case leftBrack:
		p.curErr("%#q must follow a name like a[i]", p.tok)
	case colon:
		p.curErr("ternary operator missing %#q before %#q", quest, colon)
	case _LitWord:
		l := p.getLit()
		if p.quote == paramExpArithm && p.assignIndexWords {
			p.appendAssignIndexWord(l)
		}
		if p.tok != leftBrack {
			x = p.wordOne(l)
			break
		}
		pe := &ParamExp{Short: true, Param: l}
		pe.Index = p.eitherIndex()
		x = p.wordOne(pe)
	case bckQuote:
		if p.quote == arithmExprLet && p.openBquotes > 0 {
			return nil
		}
		fallthrough
	default:
		if w := p.getWord(); w != nil {
			x = w
		} else {
			return nil
		}
	}

	if compact && p.spaced {
		return x
	}
	if !compact {
		p.got(_Newl)
	}

	// we want real nil, not (*Word)(nil) as that
	// sets the type to non-nil and then x != nil
	if p.tok == addAdd || p.tok == subSub {
		// Bash 5.3 accepts postfix `++`/`--` on any operand at
		// parse time and errors at runtime with "attempted
		// assignment to non-variable" for non-lvalues (`7++`,
		// `f()++`). Accept the parse so `for (( ; ; 7++ ))` and
		// similar expression-error test cases reach the runtime
		// check rather than aborting the surrounding parse.
		u := &UnaryArithm{
			Post:  true,
			OpPos: p.pos,
			Op:    UnAritOperator(p.tok),
			X:     x,
		}
		p.nextArith(compact)
		return u
	}
	return x
}

func (p *Parser) appendAssignIndexWord(l *Lit) {
	if !p.rawAssignIndex || p.lang.in(LangZsh) {
		for p.spaced && p.tok == _LitWord {
			next := p.getLit()
			l.Value += " " + next.Value
			l.ValueEnd = next.ValueEnd
		}
		return
	}
	for p.tok != rightBrack && p.tok != leftBrack && p.tok != _EOF {
		if p.tok == rightParen && (p.r != ']' || !rawAssignIndexOpenCommandSubst(l.Value)) {
			break
		}
		sep := ""
		if p.spaced {
			sep = " "
		}
		if p.tok == _LitWord {
			next := p.getLit()
			l.Value += sep + next.Value
			l.ValueEnd = next.ValueEnd
			continue
		}
		text := p.tok.String()
		if text == "" {
			return
		}
		l.Value += sep + text
		l.ValueEnd = posAddCol(p.pos, len(text))
		p.nextArith(false)
	}
}

func rawAssignIndexOpenCommandSubst(s string) bool {
	return strings.Count(s, "$(") > strings.Count(s, ")")
}

// nextArith consumes a token.
// It returns true if compact and the token was followed by spaces
func (p *Parser) nextArith(compact bool) bool {
	p.next()
	if compact && p.spaced {
		return true
	}
	if !compact {
		p.got(_Newl)
	}
	return false
}

func (p *Parser) nextArithOp(compact bool) {
	pos := p.pos
	tok := p.tok
	if p.nextArith(compact) {
		p.followErrExp(pos, tok)
	}
}

// arithmExprBinary is used for all left-associative binary operators
func (p *Parser) arithmExprBinary(compact bool, nextOp func(bool) ArithmExpr, operators ...BinAritOperator) ArithmExpr {
	value := nextOp(compact)
	for {
		var foundOp BinAritOperator
		for _, op := range operators {
			if p.tok == token(op) {
				foundOp = op
				break
			}
		}

		if token(foundOp) == illegalTok || (compact && p.spaced) {
			return value
		}

		if value == nil {
			p.curErr("%#q must follow an expression", p.tok)
		}

		pos := p.pos
		p.nextArithOp(compact)
		y := nextOp(compact)
		if y == nil {
			p.followErrExp(pos, foundOp)
		}

		if u, ok := value.(*UnaryArithm); ok && u.Post && arithmWordAllDigits(u.X) {
			if (u.Op == Inc && foundOp == Add) || (u.Op == Dec && foundOp == Sub) {
				value = &BinaryArithm{
					OpPos: u.OpPos,
					Op:    foundOp,
					X:     u.X,
					Y: &UnaryArithm{
						OpPos: pos,
						Op:    u.Op,
						X:     y,
					},
				}
				continue
			}
		}

		value = &BinaryArithm{
			OpPos: pos,
			Op:    foundOp,
			X:     value,
			Y:     y,
		}
	}
}

func arithmWordAllDigits(expr ArithmExpr) bool {
	word, ok := expr.(*Word)
	if !ok || len(word.Parts) != 1 {
		return false
	}
	lit, ok := word.Parts[0].(*Lit)
	if !ok || lit.Value == "" {
		return false
	}
	for _, r := range lit.Value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (p *Parser) followArithm(ftok token, fpos Pos) ArithmExpr {
	x := p.arithmExpr(false)
	if x == nil {
		if (ftok == dblLeftParen || ftok == dollDblParen) && p.peekArithmEnd() {
			return nil
		}
		p.followErrExp(fpos, ftok)
	}
	return x
}

func (p *Parser) peekArithmEnd() bool {
	if p.tok != rightParen {
		return false
	}
	if p.r == ')' {
		return true
	}
	// Bash 5.3 accepts whitespace between the two closing parens of
	// an arithmetic command/expansion: `(( true ) )` is the same as
	// `(( true ))`. Skip horizontal whitespace and re-check.
	for p.r == ' ' || p.r == '\t' {
		p.rune()
	}
	return p.r == ')'
}

func (p *Parser) arithmMatchingErr(pos Pos, left, right token) {
	switch p.tok {
	case _Lit, _LitWord:
		p.curErr("not a valid arithmetic operator: %#q", p.val)
	case leftBrack:
		p.curErr("%#q must follow a name like a[i]", leftBrack)
	case colon:
		p.curErr("ternary operator missing %#q before %#q", quest, colon)
	case rightParen, _EOF:
		p.matchingErr(pos, left, right)
	case period:
		p.checkLang(p.pos, LangZsh, `floating point arithmetic`)
	default:
		if p.quote&allArithmExpr != 0 {
			p.curErr("not a valid arithmetic operator: %#q", p.tok)
		}
		p.matchingErr(pos, left, right)
	}
}

func (p *Parser) matchedArithm(lpos Pos, left, right token) {
	if !p.got(right) {
		p.arithmMatchingErr(lpos, left, right)
	}
}

func (p *Parser) arithmEnd(ltok token, lpos Pos, old saveState) Pos {
	if !p.peekArithmEnd() {
		if p.recoverError() {
			return recoveredPos
		}
		p.arithmMatchingErr(lpos, ltok, dblRightParen)
	}
	p.rune()
	p.postNested(old)
	pos := p.pos
	p.next()
	return pos
}
