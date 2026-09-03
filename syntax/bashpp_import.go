package syntax

import (
	"slices"
	"strconv"
)

//go:generate go run gen_go127stdlib.go

// bashppImport runs only after an ordinary CallExpr has reached its
// terminator. Refusing a shape returns the original tree untouched.
func bashppImport(ce *CallExpr, redirs []*Redirect) *BashPPImport {
	if ce == nil || len(ce.Assigns) != 0 || len(redirs) != 0 || (len(ce.Args) != 2 && len(ce.Args) != 3) {
		return nil
	}
	kw := bashppBareLit(ce.Args[0])
	if kw == nil || kw.Value != "import" {
		return nil
	}
	var alias *Lit
	pathAt := 1
	if len(ce.Args) == 3 {
		alias = bashppBareLit(ce.Args[1])
		if alias == nil || !bashppImportAlias(alias.Value) {
			return nil
		}
		pathAt = 2
	}
	q, ok := exactGoImportString(ce.Args[pathAt])
	if !ok {
		return nil
	}
	path, err := strconv.Unquote(`"` + q.Parts[0].(*Lit).Value + `"`)
	if err != nil || !isGo127StdlibImport(path) {
		return nil
	}
	return &BashPPImport{Site: StartImport, Class: ClassE, Kw: kw, Alias: alias, Path: q}
}

func bashppImportAlias(name string) bool {
	return name == "_" || name == "." || bashppIsIdent(name)
}

// bashppImportGroup transactionally recognizes an exact Go import block.
// Rejection restores the ordinary Bash parser byte-for-byte.
func (p *Parser) bashppImportGroup(ce *CallExpr) Command {
	if ce == nil || len(ce.Assigns) != 0 || len(ce.Args) != 1 ||
		(p.tok != leftParen && p.tok != _Newl) {
		return nil
	}
	kw := bashppBareLit(ce.Args[0])
	if kw == nil || kw.Value != "import" {
		return nil
	}
	txn := p.beginBashPPTxn()
	var comments []Comment
	if p.tok == _Newl {
		for p.tok == _Newl {
			p.next()
		}
		if p.tok != leftParen {
			txn.rollback(p)
			return nil
		}
		comments, p.accComs = p.accComs, nil
	}
	lparen := p.pos
	p.next()
	for p.tok == _Newl {
		p.next()
	}
	var specs []*BashPPImportSpec
	var last []Comment
	for {
		if p.tok == rightParen {
			last, p.accComs = p.accComs, nil
			break
		}
		specComments := p.accComs
		p.accComs = nil
		first := p.getWord()
		if first == nil {
			txn.rollback(p)
			return nil
		}
		var alias *Lit
		pathWord := first
		if _, ok := exactGoImportString(first); !ok {
			alias = bashppBareLit(first)
			if alias == nil || !bashppImportAlias(alias.Value) {
				txn.rollback(p)
				return nil
			}
			pathWord = p.getWord()
			if pathWord == nil {
				txn.rollback(p)
				return nil
			}
		}
		path, ok := exactGoImportString(pathWord)
		if !ok {
			txn.rollback(p)
			return nil
		}
		text, err := strconv.Unquote(`"` + path.Parts[0].(*Lit).Value + `"`)
		if err != nil || !isGo127StdlibImport(text) {
			txn.rollback(p)
			return nil
		}
		specs = append(specs, &BashPPImportSpec{Comments: specComments, Alias: alias, Path: path})
		if p.tok == rightParen {
			continue
		}
		if p.tok != _Newl && p.tok != semicolon {
			txn.rollback(p)
			return nil
		}
		p.next()
		for p.tok == _Newl {
			p.next()
		}
	}
	rparen := p.pos
	p.next()
	if !bashppCallTerminator(p.tok) {
		txn.rollback(p)
		return nil
	}
	txn.commit(p)
	return &BashPPImport{
		Site: StartImport, Class: ClassR, Kw: kw, Comments: comments,
		Specs: specs, Last: last, Lparen: lparen, Rparen: rparen,
	}
}

func exactGoImportString(w *Word) (*DblQuoted, bool) {
	if w == nil || len(w.Parts) != 1 {
		return nil, false
	}
	q, ok := w.Parts[0].(*DblQuoted)
	if !ok || q.Dollar || len(q.Parts) != 1 {
		return nil, false
	}
	_, ok = q.Parts[0].(*Lit)
	return q, ok
}

func isGo127StdlibImport(path string) bool {
	_, ok := slices.BinarySearch(go127StdlibImports[:], path)
	return ok
}
