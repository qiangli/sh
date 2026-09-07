package lower

import (
	"embed"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"strings"
)

// The runtime's actual public declarations are part of the compiler build.
// Checking these declarations neither executes them nor reads installation or
// source-file paths when a generated program is compiled.
//
//go:embed shellrt/*.go
var runtimeDeclarations embed.FS

func (i bridgeImporter) importRuntime(path string) (*types.Package, error) {
	paths, err := fs.Glob(runtimeDeclarations, "shellrt/*.go")
	if err != nil {
		return nil, err
	}
	positions := token.NewFileSet()
	var files []*ast.File
	for _, name := range paths {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := runtimeDeclarations.ReadFile(name)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(positions, name, source, 0)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	config := types.Config{Importer: i.fallback, IgnoreFuncBodies: true, DisableUnusedImportCheck: true}
	return config.Check(path, positions, files, nil)
}
