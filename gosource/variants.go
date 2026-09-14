package gosource

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
)

// SourcePackageLister is what an importer may offer beyond export data: the
// on-disk Go source files of a package (build-constrained, no cgo, no test
// files), so the map importer can re-check a dependency against the explicit
// package map instead of reading export data that was compiled against
// something else.
type SourcePackageLister interface {
	SourcePackageFiles(path, srcDir string) (dir string, files []string, err error)
}

// variantImport is cmd/go's test-variant rule, applied to export data: a
// dependency read through the fallback whose (transitive) imports name a
// mapped path was compiled against the ON-DISK package of that path, so its
// types would be a second, unrelated object — `types.Importer` from
// go/importer's export data is not the `types.Importer` of the ptest
// go/types the external test package is checked against. cmd/go rebuilds
// every such dependent against the test variant; here it is re-checked from
// its own source with this map importer, so every reference binds to the
// mapped object. A dependency that reaches no mapped path keeps its export
// data, and one without listable source (cgo, no lister) is left as read.
func (m *mapImporter) variantImport(path string, pkg *types.Package, srcDir string) (*types.Package, error) {
	if len(m.packages) == 0 || m.variants == nil {
		return pkg, nil
	}
	if v, ok := m.variants[path]; ok && v != nil {
		return v, nil
	}
	if !m.reachesMapped(pkg, map[*types.Package]bool{}) {
		return pkg, nil
	}
	lister, ok := m.fallback.(SourcePackageLister)
	if !ok {
		return pkg, nil
	}
	dir, names, err := lister.SourcePackageFiles(path, srcDir)
	if err != nil || len(names) == 0 {
		return pkg, nil
	}
	if m.variants[path] == nil && m.variantBusy[path] {
		return nil, fmt.Errorf("import cycle through the explicit package map at %q", path)
	}
	m.variantBusy[path] = true
	defer delete(m.variantBusy, path)
	sort.Strings(names)
	var files []*ast.File
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(m.fset, filepath.Join(dir, name), data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	// The variant's own imports are decided with the variant as the importer
	// (go/importer may import go/internal/gcimporter), never the program.
	savedFrom, savedIdentity := m.from, m.identity
	m.from, m.identity = path, importerIdentity{declared: true}
	defer func() { m.from, m.identity = savedFrom, savedIdentity }()
	var errs []error
	config := m.checker.config(m, func(err error) { errs = append(errs, err) })
	config.IgnoreFuncBodies = true
	checked, err := config.Check(path, m.fset, files, nil)
	if err != nil || len(errs) > 0 {
		if err == nil {
			err = errs[0]
		}
		return nil, fmt.Errorf("gosource: re-checking %q against the explicit package map: %v", path, err)
	}
	m.variants[path] = checked
	return checked, nil
}

// reachesMapped reports whether pkg or any package it imports, transitively,
// is in the explicit map.
func (m *mapImporter) reachesMapped(pkg *types.Package, seen map[*types.Package]bool) bool {
	if pkg == nil || seen[pkg] {
		return false
	}
	seen[pkg] = true
	for _, imp := range pkg.Imports() {
		if _, ok := m.packages[imp.Path()]; ok {
			return true
		}
		if m.reachesMapped(imp, seen) {
			return true
		}
	}
	return false
}

var _ = token.NoPos
