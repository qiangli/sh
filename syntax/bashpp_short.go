// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package syntax

import (
	"bytes"
	"io"
	"strings"
)

// bashppShortDecl recognizes the Class-E half of := after the ordinary shell
// parser has completed the command. No speculative input is involved here.
func bashppShortDecl(ce *CallExpr, redirs []*Redirect) *BashPPShortDecl {
	if ce == nil || len(ce.Assigns) != 0 || len(redirs) != 0 {
		return nil
	}
	op := -1
	for i, w := range ce.Args {
		if l := bashppBareLit(w); l != nil && l.Value == ":=" {
			if op >= 0 {
				return nil
			}
			op = i
		}
	}
	if op < 1 || op+1 >= len(ce.Args) {
		return nil
	}
	lhs, ok := bashppShortLHS(ce.Args[:op])
	if !ok {
		return nil
	}
	rhs, ok := bashppShortValues(ce.Args[op+1:])
	if !ok || (len(lhs) > 1 && len(lhs) != len(rhs)) {
		return nil
	}
	m := RecognizeStartSite(shortDeclHead(lhs) + " := " + bashppWordText(rhs[0]))
	if m.Site != StartShortDecl || m.Class != ClassE {
		return nil
	}
	d := &BashPPShortDecl{Lhs: lhs, Rhs: rhs, Class: ClassE, OpPos: ce.Args[op].Pos()}
	if len(rhs) == 1 {
		if lit := bashppBareLit(rhs[0]); lit != nil && strings.Contains(lit.Value, ".") && bashppSelector(lit.Value) {
			d.MethodValue = bashppSelectorLits(lit)
		}
	}
	return d
}

func bashppShortLHS(words []*Word) ([]*Lit, bool) {
	var out []*Lit
	for i, w := range words {
		l := bashppBareLit(w)
		if l == nil {
			return nil, false
		}
		value := l.Value
		if i < len(words)-1 {
			if !strings.HasSuffix(value, ",") {
				return nil, false
			}
			value = strings.TrimSuffix(value, ",")
		} else if strings.HasSuffix(value, ",") {
			return nil, false
		}
		if !bashppIsIdent(value) {
			return nil, false
		}
		out = append(out, &Lit{ValuePos: l.ValuePos, ValueEnd: posAddCol(l.ValuePos, len(value)), Value: value})
	}
	return out, len(out) > 0
}

func bashppShortValues(words []*Word) ([]*Word, bool) {
	if len(words) > 1 {
		joined := bashppJoinWords(words)
		if bashppSupportedValue(joined) {
			return []*Word{joined}, true
		}
	}
	out := make([]*Word, len(words))
	for i, w := range words {
		wantComma := i < len(words)-1
		clean, comma := bashppTrimComma(w)
		if comma != wantComma || !bashppSupportedValue(clean) {
			return nil, false
		}
		out[i] = clean
	}
	return out, len(out) > 0
}

func bashppJoinWords(words []*Word) *Word {
	parts := make([]WordPart, 0, len(words)*2-1)
	for i, w := range words {
		if i > 0 {
			parts = append(parts, &Lit{ValuePos: words[i-1].End(), ValueEnd: w.Pos(), Value: " "})
		}
		parts = append(parts, w.Parts...)
	}
	return &Word{Parts: parts}
}

func bashppTrimComma(w *Word) (*Word, bool) {
	if w == nil || len(w.Parts) == 0 {
		return w, false
	}
	last, ok := w.Parts[len(w.Parts)-1].(*Lit)
	if !ok || !strings.HasSuffix(last.Value, ",") {
		return w, false
	}
	copyWord := *w
	copyWord.Parts = append([]WordPart(nil), w.Parts...)
	copyLit := *last
	copyLit.Value = strings.TrimSuffix(copyLit.Value, ",")
	copyLit.ValueEnd = posAddCol(copyLit.ValueEnd, -1)
	if copyLit.Value == "" {
		copyWord.Parts = copyWord.Parts[:len(copyWord.Parts)-1]
	} else {
		copyWord.Parts[len(copyWord.Parts)-1] = &copyLit
	}
	return &copyWord, true
}

func bashppSupportedValue(w *Word) bool {
	if bashppInitKind(w) != "" {
		return true
	}
	if len(w.Parts) == 1 {
		switch q := w.Parts[0].(type) {
		case *SglQuoted:
			return !q.Dollar
		case *DblQuoted:
			for _, part := range q.Parts {
				if _, ok := part.(*Lit); !ok {
					return false
				}
			}
			return true
		}
	}
	text := bashppWordText(w)
	if strings.Contains(text, ".") && bashppSelector(text) {
		return true
	}
	if open := strings.IndexByte(text, '{'); open >= 0 {
		return strings.HasSuffix(text, "}") && bashppCompositeType(text[:open]) &&
			bashppBalanced(text[open:], '{', '}')
	}
	if open := strings.IndexByte(text, '['); open > 0 {
		return strings.HasSuffix(text, "]") && bashppSelector(text[:open]) &&
			bashppNonemptyTypeArgs(text[open:])
	}
	return false
}

func bashppCompositeType(s string) bool {
	if bashppSelector(s) {
		return true
	}
	if strings.HasPrefix(s, "[]") {
		return bashppSelector(s[2:])
	}
	if !strings.HasPrefix(s, "map[") {
		return false
	}
	close := strings.IndexByte(s, ']')
	return close > len("map[") && bashppSelector(s[len("map["):close]) &&
		bashppSelector(s[close+1:])
}

func bashppNonemptyTypeArgs(s string) bool {
	if len(s) < 3 || s[0] != '[' || s[len(s)-1] != ']' || !bashppBalanced(s, '[', ']') {
		return false
	}
	for _, arg := range strings.Split(s[1:len(s)-1], ",") {
		if !bashppSelector(strings.TrimSpace(arg)) {
			return false
		}
	}
	return true
}

func bashppBalanced(s string, open, close byte) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case open:
			depth++
		case close:
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func bashppWordText(w *Word) string {
	var b bytes.Buffer
	_ = NewPrinter().Print(&b, w)
	return b.String()
}

func shortDeclHead(lhs []*Lit) string {
	var b strings.Builder
	for i, l := range lhs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(l.Value)
	}
	return b.String()
}

func bashppSelector(s string) bool {
	return s != "" && leadingSelector(s) == s
}

func bashppSelectorLits(name *Lit) []*Lit {
	parts := strings.Split(name.Value, ".")
	out := make([]*Lit, len(parts))
	off := 0
	for i, part := range parts {
		pos := posAddCol(name.ValuePos, off)
		out[i] = &Lit{ValuePos: pos, ValueEnd: posAddCol(pos, len(part)), Value: part}
		off += len(part) + 1
	}
	return out
}

// parserTransaction records reads beyond the lexer's current buffer and keeps
// a complete parser snapshot. Rollback restores both, including the original
// byte positions and pending diagnostic state.
type parserTransaction struct {
	saved Parser
	src   io.Reader
	read  *recordingReader
	bsLen int
}

type recordingReader struct {
	r io.Reader
	b bytes.Buffer
}

func (r *recordingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		_, _ = r.b.Write(p[:n])
	}
	return n, err
}

func (p *Parser) beginBashPPTxn() *parserTransaction {
	t := &parserTransaction{saved: *p, src: p.src, bsLen: len(p.bs)}
	t.read = &recordingReader{r: p.src}
	p.src = t.read
	return t
}

func (t *parserTransaction) commit(p *Parser) { p.src = t.src }

func (t *parserTransaction) rollback(p *Parser) {
	*p = t.saved
	if t.bsLen == 0 {
		p.bs = nil
	} else {
		p.bs = p.readBuf[:t.bsLen]
	}
	if t.read.b.Len() > 0 {
		p.src = io.MultiReader(bytes.NewReader(t.read.b.Bytes()), t.src)
	} else {
		p.src = t.src
	}
}

// bashppParenForm recognizes a Class-R call transactionally. Any malformed or
// unsupported candidate is rewound and left to the ordinary Bash parser.
func (p *Parser) bashppParenForm(ce *CallExpr) Command {
	if ce == nil || len(ce.Assigns) != 0 || len(ce.Args) == 0 || p.tok != leftParen || p.spaced {
		return nil
	}
	// A `func` in callee position is a function LITERAL, not a call to
	// something named func — `func` is a Go keyword and can never be a callee.
	// It is claimed here rather than at a fourth call site in parser.go
	// because every literal site reaches this same point.
	if cmd := p.bashppFuncLitForm(ce); cmd != nil {
		return cmd
	}
	var name *Lit
	var lhs []*Lit
	var opPos Pos
	short := false
	if len(ce.Args) >= 3 {
		op := len(ce.Args) - 2
		opLit, funLit := bashppBareLit(ce.Args[op]), bashppBareLit(ce.Args[op+1])
		var ok bool
		if opLit == nil || opLit.Value != ":=" || funLit == nil {
			return nil
		}
		lhs, ok = bashppShortLHS(ce.Args[:op])
		if !ok {
			return nil
		}
		name, opPos, short = funLit, opLit.Pos(), true
	} else if len(ce.Args) == 1 {
		name = bashppBareLit(ce.Args[0])
	} else {
		return nil
	}
	if name == nil || !bashppSelector(name.Value) {
		return nil
	}
	if !short && !strings.Contains(name.Value, ".") && p.r == ')' && !p.bashppCallable(name.Value) {
		// `f()` is also the prefix of a classic shell function definition.
		// Only a previously declared Bash++ function makes the zero-argument
		// call unambiguous; calls with arguments remain unambiguous Class R.
		return nil
	}

	txn := p.beginBashPPTxn()
	lparen := p.pos
	p.next()
	args, ellipsis, ok := p.bashppCallArgs()
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
	call := &BashPPCall{
		Fun: bashppSelectorLits(name), Args: args, Ellipsis: ellipsis,
		Lparen: lparen, Rparen: rparen,
	}
	if !short {
		return call
	}
	// A name bound from a call may hold a closure — that is the factory idiom,
	// `next := counter()` — so it joins the callable names for the rest of the
	// parse. The claim is narrow in practice: it only decides the ZERO-argument
	// `next()`, which bash rejects outright, and the call form still rewinds
	// when a body follows, so `next() { … }` and `next() ( … )` stay the shell
	// function definitions they are today.
	for _, lit := range lhs {
		p.bashppRegisterFunc(lit.Value)
	}
	var text strings.Builder
	text.WriteString(name.Value)
	text.WriteByte('(')
	for i, arg := range args {
		if i > 0 {
			text.WriteString(", ")
		}
		text.WriteString(bashppWordText(arg))
	}
	if ellipsis.IsValid() {
		text.WriteString("...")
	}
	text.WriteByte(')')
	rhs := &Word{Parts: []WordPart{&Lit{ValuePos: name.Pos(), ValueEnd: call.End(), Value: text.String()}}}
	return &BashPPShortDecl{Lhs: lhs, Rhs: []*Word{rhs}, Class: ClassR, OpPos: opPos, Call: call}
}

func bashppCallTerminator(tok token) bool {
	switch tok {
	case _EOF, _Newl, semicolon:
		return true
	}
	return false
}

// bashppCallArgs reads a call's argument list up to the closing parenthesis,
// returning the position of a trailing `...` when the final argument spreads a
// slice into a variadic parameter. Go allows the dots only on the last
// argument, so one position describes the whole list.
func (p *Parser) bashppCallArgs() ([]*Word, Pos, bool) {
	if p.tok == rightParen {
		return nil, Pos{}, true
	}
	var args []*Word
	for {
		w := p.getWord()
		if w == nil {
			return nil, Pos{}, false
		}
		clean, comma := bashppTrimComma(w)
		clean, ellipsis := bashppTrimEllipsis(clean)
		if !bashppCallArg(clean) {
			return nil, Pos{}, false
		}
		args = append(args, clean)
		if ellipsis.IsValid() && (comma || p.tok != rightParen) {
			// `f(xs..., y)` — the dots were not on the final argument.
			return nil, Pos{}, false
		}
		if p.tok == rightParen {
			return args, ellipsis, !comma
		}
		if !comma {
			return nil, Pos{}, false
		}
	}
}

// bashppTrimEllipsis splits a trailing `...` off an argument word, returning
// the word without it and the position the dots occupied. It mirrors
// [bashppTrimComma]: both peel a piece of punctuation the shell lexer has
// glued onto the end of a word, and both must leave the word's own positions
// intact so the printer can put the source back together.
func bashppTrimEllipsis(w *Word) (*Word, Pos) {
	if w == nil || len(w.Parts) == 0 {
		return w, Pos{}
	}
	last, ok := w.Parts[len(w.Parts)-1].(*Lit)
	if !ok || !strings.HasSuffix(last.Value, "...") {
		return w, Pos{}
	}
	if last.Value == "..." && len(w.Parts) == 1 {
		// A bare `...` spreads nothing; leave it to the shell.
		return w, Pos{}
	}
	pos := posAddCol(last.ValueEnd, -3)
	copyWord := *w
	copyWord.Parts = append([]WordPart(nil), w.Parts...)
	copyLit := *last
	copyLit.Value = strings.TrimSuffix(copyLit.Value, "...")
	copyLit.ValueEnd = pos
	if copyLit.Value == "" {
		copyWord.Parts = copyWord.Parts[:len(copyWord.Parts)-1]
	} else {
		copyWord.Parts[len(copyWord.Parts)-1] = &copyLit
	}
	return &copyWord, pos
}

func bashppCallArg(w *Word) bool {
	if bashppSupportedValue(w) {
		return true
	}
	if l := bashppBareLit(w); l != nil {
		return bashppIsIdent(l.Value)
	}
	// A call that actually runs needs to pass values, not only literals, so a
	// parameter expansion (`$x`), an arithmetic expansion (`$((n-1))`) or a
	// quoted word carrying one is a valid argument. Command and process
	// substitution stay excluded: they run a command, and the published call
	// grammar keeps a Class R argument list side-effect-free, so `f($(x))`
	// rolls back to the shell rather than being claimed.
	return bashppArgWordSafe(w)
}

// bashppArgWordSafe reports whether every part of w is a value-producing form
// that does not execute a command. It is recursive so a double-quoted word is
// only safe when its own parts are.
func bashppArgWordSafe(w *Word) bool {
	if len(w.Parts) == 0 {
		return false
	}
	for _, part := range w.Parts {
		if !bashppArgPartSafe(part) {
			return false
		}
	}
	return true
}

func bashppArgPartSafe(part WordPart) bool {
	switch part := part.(type) {
	case *Lit, *SglQuoted, *ParamExp, *ArithmExp:
		return true
	case *DblQuoted:
		for _, inner := range part.Parts {
			if !bashppArgPartSafe(inner) {
				return false
			}
		}
		return true
	}
	return false
}
