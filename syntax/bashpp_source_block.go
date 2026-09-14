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
	if !BashPPValidIdent(language) {
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
		if fields[0] != "as" || !BashPPValidIdent(fields[1]) {
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
			if language == "python" && alias == "" {
				p.bashppRegisterSourceBlockFuncs(block.Body)
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
// from a shell function header. The Python AST analyzer remains authoritative
// for declarations, visibility, signatures, and diagnostics.
func (p *Parser) bashppRegisterSourceBlockFuncs(body string) {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "def ") {
			continue
		}
		name, _, ok := strings.Cut(strings.TrimPrefix(line, "def "), "(")
		name = strings.TrimSpace(name)
		if ok && !strings.HasPrefix(name, "_") && BashPPValidIdent(name) {
			p.bashppRegisterFunc(name)
		}
	}
}
