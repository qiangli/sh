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
		if alias == nil || alias.Value == "_" || !bashppIsIdent(alias.Value) {
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
