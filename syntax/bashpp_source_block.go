package syntax

import (
	"strings"
	"unicode/utf8"
)

// bashppSourceBlock claims only a complete fence header. Near misses return to
// ordinary shell parsing unchanged because this is a Class E start site.
func (p *Parser) bashppSourceBlock() *SourceBlock {
	opener, pos := p.val, p.pos
	n := 0
	for n < len(opener) && opener[n] == '~' {
		n++
	}
	if n < 3 || n == len(opener) {
		return nil
	}
	language := opener[n:]
	if !bashppValidSourceLanguage(language) {
		return nil
	}
	fence := opener[:n]

	var tail strings.Builder
	for p.r != '\n' && p.r != utf8.RuneSelf {
		tail.WriteRune(p.r)
		p.rune()
	}
	fields := strings.Fields(tail.String())
	var alias string
	switch len(fields) {
	case 0:
	case 2:
		if fields[0] != "as" || !bashppValidSourceAlias(language, fields[1]) {
			return nil
		}
		alias = fields[1]
	default:
		return nil
	}
	langPos := posAddCol(pos, n)
	block := &SourceBlock{
		Fence: fence, FencePos: pos,
		Language: &Lit{ValuePos: langPos, ValueEnd: posAddCol(langPos, len(language)), Value: language},
	}
	if alias != "" {
		aliasPos := posAddCol(pos, len(opener)+len(" as "))
		block.Alias = &Lit{ValuePos: aliasPos, ValueEnd: posAddCol(aliasPos, len(alias)), Value: alias}
	}
	if p.r == utf8.RuneSelf {
		p.posErr(pos, "unclosed source block %q", fence)
		return block
	}

	p.rune()
	block.BodyPos = p.nextPos()
	var body strings.Builder
	for p.r != utf8.RuneSelf {
		linePos := p.nextPos()
		var line strings.Builder
		for p.r != '\n' && p.r != utf8.RuneSelf {
			line.WriteRune(p.r)
			p.rune()
		}
		if line.String() == fence {
			block.Body = body.String()
			block.ClosingPos = linePos
			if alias == "" {
				p.bashppRegisterSourceBlockFuncs(language, block.Body)
			}
			if p.r == '\n' {
				p.tok = _Newl
			} else {
				p.tok = _EOF
			}
			return block
		}
		body.WriteString(line.String())
		if p.r == '\n' {
			body.WriteByte('\n')
			p.rune()
		}
	}
	block.Body = body.String()
	p.tok = _EOF
	p.posErr(pos, "unclosed source block %q", fence)
	return block
}

// bashppRegisterSourceBlockFuncs supplies the parser only with the small bit
// of look-ahead it needs to distinguish a later zero-argument direct call
// from a shell function header. The language's own analyzer remains
// authoritative for declarations, visibility, signatures, and diagnostics.
// The per-language line reader comes from the fence look-ahead table; a
// language without a row (every text fence, which promotes nothing) needs
// none.
func (p *Parser) bashppRegisterSourceBlockFuncs(language, body string) {
	lookahead := bashppFenceLookahead[strings.ToLower(language)]
	if lookahead == nil {
		return
	}
	for _, line := range strings.Split(body, "\n") {
		declaration := lookahead(line)
		if declaration == "" {
			continue
		}
		name, _, ok := strings.Cut(declaration, "(")
		name = strings.TrimSpace(name)
		if ok && !strings.HasPrefix(name, "_") && BashPPValidIdent(name) {
			p.bashppRegisterFunc(name)
		}
	}
}

// A fence language is a lexical identifier, not a Go declaration name. The Go
// adapter therefore legitimately uses the otherwise-reserved word "go".
func bashppValidSourceLanguage(language string) bool {
	return BashPPValidIdent(language) || language == "go"
}

// A Go fence may use the natural `go` qualifier promised by the dag front
// door. Keep that one contextual spelling local to source fences; `go`
// remains invalid for every Bash++ declaration and ordinary callable.
func bashppValidSourceAlias(language, alias string) bool {
	return BashPPValidIdent(alias) || strings.EqualFold(language, "go") && alias == "go"
}
