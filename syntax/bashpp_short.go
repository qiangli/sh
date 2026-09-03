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
	return &BashPPShortDecl{Lhs: lhs, Rhs: rhs, Class: ClassE, OpPos: ce.Args[op].Pos()}
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
	if name == nil || !bashppSelector(name.Value) || (!short && !strings.Contains(name.Value, ".") && p.r == ')') {
		return nil
	}

	txn := p.beginBashPPTxn()
	lparen := p.pos
	p.next()
	args, ok := p.bashppCallArgs()
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
	call := &BashPPCall{Fun: bashppSelectorLits(name), Args: args, Lparen: lparen, Rparen: rparen}
	if !short {
		return call
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
	text.WriteByte(')')
	rhs := &Word{Parts: []WordPart{&Lit{ValuePos: name.Pos(), ValueEnd: call.End(), Value: text.String()}}}
	return &BashPPShortDecl{Lhs: lhs, Rhs: []*Word{rhs}, Class: ClassR, OpPos: opPos}
}

func bashppCallTerminator(tok token) bool {
	switch tok {
	case _EOF, _Newl, semicolon:
		return true
	}
	return false
}

func (p *Parser) bashppCallArgs() ([]*Word, bool) {
	if p.tok == rightParen {
		return nil, true
	}
	var args []*Word
	for {
		w := p.getWord()
		if w == nil {
			return nil, false
		}
		clean, comma := bashppTrimComma(w)
		if !bashppCallArg(clean) {
			return nil, false
		}
		args = append(args, clean)
		if p.tok == rightParen {
			return args, !comma
		}
		if !comma {
			return nil, false
		}
	}
}

func bashppCallArg(w *Word) bool {
	if bashppSupportedValue(w) {
		return true
	}
	if l := bashppBareLit(w); l != nil {
		return bashppIsIdent(l.Value)
	}
	return false
}
