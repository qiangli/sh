package gosource

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"strconv"
	"strings"
)

// importPathError is a compiler-front-end diagnostic. Import path validity is
// checked before import resolution: a malformed path must not be turned into
// an importer-dependent "could not import" failure.
type importPathError struct {
	pos token.Position
	msg string
}

func (e importPathError) Error() string {
	return fmt.Sprintf("%s: %s", e.pos, e.msg)
}

// validateImportPaths implements the compiler's path-normal-form checks that
// go/types deliberately leaves to an importer. The checks apply to every
// parsed package, independently of the selected importer or package map.
func validateImportPaths(fset *token.FileSet, files []*ast.File) ErrorList {
	var out ErrorList
	for _, file := range files {
		for _, spec := range file.Imports {
			value, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue // gc's scanner/parser owns malformed string literals.
			}
			pos := fset.Position(spec.Path.Pos())
			// go/types already owns the empty-path diagnostic and continues
			// checking its sibling imports. Only normal-form differences would
			// otherwise reach importer-dependent resolution.
			// gc (noder/import.go openPackage) exempts local names — paths
			// beginning with "./", "../" or "/" — from the canonical-form
			// check; only non-local paths must already be clean.
			if value != "" && !isLocalImportName(value) && path.Clean(value) != value {
				out = append(out, importPathError{pos, fmt.Sprintf("non-canonical import path %q (should be %q)", value, path.Clean(value))})
			}
		}
	}
	return out
}

// isLocalImportName mirrors gc's islocalname: a path that begins with "./",
// "../" or "/" (or is exactly "." / "..") is resolved relative to a directory
// and is never canonicalised.
func isLocalImportName(name string) bool {
	return strings.HasPrefix(name, "/") ||
		strings.HasPrefix(name, "./") || name == "." ||
		strings.HasPrefix(name, "../") || name == ".."
}
