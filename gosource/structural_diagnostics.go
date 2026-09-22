package gosource

import (
	"fmt"

	gcsyntax "mvdan.cc/sh/v3/gosource/internal/gcsyntax"
)

// structuralCheckerDiagnostics recovers checker diagnostics which go/types
// cannot produce from go/parser's recovered tree. The gc parser retains the
// operand of a non-call go/defer statement in a CallStmt, while go/parser
// replaces the entire statement with BadStmt after reporting a parser error.
// types2 consequently diagnoses the retained operand and continues checking
// the rest of the function independently.
func structuralCheckerDiagnostics(files []*gcsyntax.File) ErrorList {
	var diagnostics ErrorList
	for _, file := range files {
		if file == nil {
			continue
		}
		gcsyntax.Inspect(file, func(node gcsyntax.Node) bool {
			stmt, ok := node.(*gcsyntax.CallStmt)
			if !ok || stmt == nil || stmt.Call == nil {
				return true
			}
			if _, ok := stmt.Call.(*gcsyntax.CallExpr); ok {
				return true
			}
			keyword := stmt.Tok.String()
			diagnostics = append(diagnostics, gcError{
				pos: gcsyntax.EndPos(stmt.Call),
				msg: fmt.Sprintf("expression in %s must be function call", keyword),
			})
			return true
		})
	}
	return diagnostics
}

// appendStructuralCheckerDiagnostics adds only diagnostics absent because
// go/parser discarded their statement. gcError is comparable, so the exact
// position and wording are used for deduplication; unrelated errors on the
// same line are deliberately preserved.
func appendStructuralCheckerDiagnostics(out ErrorList, files []*gcsyntax.File) ErrorList {
	for _, recovered := range structuralCheckerDiagnostics(files) {
		duplicate := false
		for _, existing := range out {
			if existing == recovered {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, recovered)
		}
	}
	return out
}
