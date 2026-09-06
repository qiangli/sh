// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

// This file holds the one carrier that lets a Go scalar expression written
// with SHELL METACHARACTERS reach the typed [BashPPExpr] tree.
//
// WHY A CARRIER IS NEEDED AT ALL. [bashppScalarExpr] translates the words a
// completed command already produced; it never re-lexes. That is why only the
// operators the shell lexer keeps inside a word — `+ - * / % ^ == !=` — were
// reachable from source. The remaining Go binary operators are spelled with
// characters the lexer owns, so the shell parser had already taken them apart
// before the recognizer ever ran:
//
//	x := true || false   BinaryCmd{Or, …}          — two statements
//	x := 1 & 2           CallExpr + Background     — two statements
//	x := 1 | 2           BinaryCmd{Pipe, …}        — a pipeline
//	x := 1 < 2           CallExpr + Redirect{<}    — a redirect
//	x := 1 <= 2          CallExpr + Redirect{<, "="} + arg "2"
//	x := 1 << 2          Redirect{<<} — a HEREDOC, which then reads lines
//
// A recognizer that ran after the fact could not repair those: the pipeline
// and binary-command splits happen above [Parser.callExpr], and `<<` has
// already consumed the rest of the input as a here-document body.
//
// WHAT THE CARRIER DOES. Inside an already committed Go region, and only once
// the words parsed so far are a recognized short-declaration head followed by
// at least one operand, an operator token is taken over BEFORE the redirect
// and terminator machinery sees it. The token contributes one [Lit] with its
// exact source positions to the argument list, and word collection resumes.
// The two-character forms the lexer cannot spell — `<=`, `>=` and `&^` — need
// nothing extra: their tail arrives as the adjacent word `=` or `^`, and
// [bashppJoinWords] keeps adjacent words adjacent.
//
// Nothing is decided from a prefix. The whole tail is read inside a
// [parserTransaction], and unless [bashppShortDecl] then yields an exact-Go
// scalar expression the transaction is rolled back — parser state, byte
// positions and unread input included — leaving the operator token exactly
// where it was for the ordinary shell path to consume. So a near miss such as
// `x := 1 <` or `out := $(cmd) > file` keeps its bash meaning, byte for byte.
//
// WHY IT IS GATED ON AN OPEN FUNC BODY. `x := 1 < 2` is legal bash today: it
// runs `x` with arguments `:=` and `1`, reading stdin from the file `2`. Only
// inside a Bash++ func body is there nothing to protect — `func f(…) { … }` is
// itself Class R, so bash never reaches the body — which is the same reasoning
// [bashppChanForm] and [Parser.bashppReturn] rely on. Top-level Class-E input,
// LangBash, LangPOSIX and every other variant are untouched by construction.

// bashppScalarOpTok reports the source spelling of an operator token the
// carrier may take over, or "" for every other token. `<=`, `>=` and `&^` are
// absent on purpose: the lexer hands their first character over as `<`, `>` or
// `&` and leaves the rest as an adjacent word.
//
// A digit-prefixed redirect (`1<2`, lexed as _LitRedir) is likewise absent.
// That spelling is an ordinary file-descriptor redirect in every shell, and
// claiming it would be a reinterpretation rather than a new reach.
func bashppScalarOpTok(tok token) string {
	switch tok {
	case orOr:
		return "||"
	case andAnd:
		return "&&"
	case or:
		return "|"
	case and:
		return "&"
	case rdrIn:
		return "<"
	case rdrOut:
		return ">"
	case hdoc:
		return "<<"
	case appOut:
		return ">>"
	}
	return ""
}

// bashppScalarHead reports whether the words parsed so far are a recognized
// short-declaration head — `x :=` or `x, y :=` — followed by at least one
// operand word. The trailing operand is what separates an expression from the
// forms whose right-hand side is the operator itself, so `v := <-ch` and
// `v, ok := <-ch` never arm the carrier and stay channel receives.
func bashppScalarHead(ce *CallExpr) bool {
	if ce == nil || len(ce.Assigns) != 0 {
		return false
	}
	op := -1
	for i, w := range ce.Args {
		if l := bashppBareLit(w); l != nil && l.Value == ":=" {
			if op >= 0 {
				return false
			}
			op = i
		}
	}
	if op < 1 || op+1 >= len(ce.Args) {
		return false
	}
	_, ok := bashppShortLHS(ce.Args[:op])
	return ok
}

// bashppScalarTail reads the rest of a scalar expression, starting at an
// operator token, and returns the completed declaration. It returns nil for
// every shape it does not claim, having first restored the parser to the state
// it was handed.
func (p *Parser) bashppScalarTail(ce *CallExpr) *BashPPShortDecl {
	txn := p.beginBashPPTxn()
	argc := len(ce.Args)
	if decl := p.bashppScalarTailWords(ce); decl != nil {
		txn.commit(p)
		return decl
	}
	txn.rollback(p)
	ce.Args = ce.Args[:argc]
	return nil
}

func (p *Parser) bashppScalarTailWords(ce *CallExpr) *BashPPShortDecl {
	for {
		op := bashppScalarOpTok(p.tok)
		if op == "" {
			return nil
		}
		ce.Args = append(ce.Args, p.wordOne(&Lit{
			ValuePos: p.pos, ValueEnd: posAddCol(p.pos, len(op)), Value: op,
		}))
		p.next()
		for !p.bashppScalarTailEnd() && bashppScalarOpTok(p.tok) == "" {
			w := p.bashppScalarOperand()
			if w == nil {
				return nil
			}
			ce.Args = append(ce.Args, w)
		}
		if p.bashppScalarTailEnd() {
			break
		}
	}
	decl := bashppShortDecl(ce, nil, true)
	if decl == nil || decl.Expr == nil {
		return nil
	}
	return decl
}

// bashppScalarTailEnd reports the statement terminators an expression may run
// up to. `}` is one of them because Go's semicolon-insertion rule closes the
// statement at the func body's own brace, which the shell lexer hands over as
// an ordinary word; see the matching case in [Parser.callExpr].
func (p *Parser) bashppScalarTailEnd() bool {
	switch p.tok {
	case _EOF, _Newl, semicolon:
		return true
	}
	return p.tok == _LitWord && p.val == "}"
}

// bashppScalarOperand reads one operand word, using the same readers the
// ordinary command loop uses for the same tokens. Anything else — a
// parenthesis, a redirect the carrier does not spell, a `;;` — returns nil and
// rolls the whole tail back.
func (p *Parser) bashppScalarOperand() *Word {
	switch p.tok {
	case _LitWord:
		w := p.wordOne(p.lit(p.pos, p.val))
		p.next()
		return w
	case _Lit, sglQuote, dblQuote:
		return p.wordAnyNumber()
	}
	return nil
}
