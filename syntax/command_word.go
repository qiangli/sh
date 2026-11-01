package syntax

// CommandWordObserver observes unquoted literal words at command position,
// before their command is parsed. Returning an error stops parsing. This lets
// an interpreter request a fresh parse after a grammar-changing alias expands,
// while the shell lexer continues to own quoting, comments and here-documents.
// Reserved words, command substitutions and non-command arguments are not observed. The observer may
// see a word more than once; it must not have execution side effects.
func CommandWordObserver(fn func(string, Pos) error) ParserOption {
	return func(p *Parser) { p.commandWordObserver = fn }
}

func (p *Parser) observeCommandWord() bool {
	if p.err != nil {
		return false
	}
	if p.commandWordObserver == nil || p.tok != _LitWord || p.openComsubs > 0 || p.openBquotes > 0 {
		return true
	}
	switch p.val {
	case "!", "{", "}", "if", "then", "elif", "else", "fi", "while", "until", "do", "done", "for", "select", "case", "in", "esac", "function", "time", "coproc", "[[", "]]":
		return true
	}
	if err := p.commandWordObserver(p.val, p.pos); err != nil {
		p.err = err
		return false
	}
	return true
}
