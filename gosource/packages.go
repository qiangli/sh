package gosource

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"sort"
	"strings"
)

// PackageSpec is one explicitly supplied dependency package: the import path
// it is registered under and the exact source files that make it up. The
// path is an opaque identity, exactly as `go tool compile -p` treats it; it
// is never looked up on disk.
type PackageSpec struct {
	Path    string
	Sources []Source
}

// Resolution is one recorded import resolution. Load records every import
// the type checker asked for, in the order it asked, so the sequence is
// deterministic for a given set of inputs and can be listed and audited.
type Resolution struct {
	// From is the import path of the package whose file contained the import.
	From string
	// Import is the path exactly as written in the source.
	Import string
	// Path is the path after relative-import joining; equal to Import for a
	// non-relative import.
	Path string
	// Origin is "package-map" when the explicit map satisfied the import and
	// "importer" when the caller's importer (module, GOPATH or export data)
	// did. A rejected import is not recorded; it is reported as a diagnostic.
	Origin string
	// Name is the resolved package's declared name.
	Name string
	// Files lists the explicit map package's source names, sorted; nil for
	// an importer resolution.
	Files []string
}

// mapImporter implements the policy-free half of Go's import model: an
// explicit import-path→package map consulted before any on-disk policy, plus
// the compiler's `-D` rule for relative imports. It mirrors what
// `go tool compile -importcfg -D` does with `.a` files, in memory.
type mapImporter struct {
	base        string
	packages    map[string]*types.Package
	files       map[string][]string
	fallback    types.Importer
	from        string
	resolutions []Resolution
	// checked retains what checkDependency parsed and checked, keyed by
	// path, and order lists the paths as they were checked. Load links
	// each checked package into the lowered file from exactly this data:
	// the ASTs parsed from the caller's bytes, positioned in the shared
	// file set, and the checker's Info over them.
	checked map[string]*checkedPackage
	order   []string
}

// checkedPackage is one explicit package after checkDependency: its sorted
// sources, their ASTs, the checker's Info over them and the resulting
// package object, which is the same pointer every importer of it receives.
type checkedPackage struct {
	spec    PackageSpec
	sources []Source
	files   []*ast.File
	info    *types.Info
	pkg     *types.Package
}

func newMapImporter(base string, fallback types.Importer) *mapImporter {
	return &mapImporter{base: base, packages: map[string]*types.Package{}, files: map[string][]string{}, fallback: fallback, checked: map[string]*checkedPackage{}}
}

// newTypeInfo allocates the Info map set the converter reads. The program
// and every explicit package are checked into an Info of this shape so the
// same converter can lower any of them. Instances is what lets the converter
// spell every generic call's type arguments explicitly, inferred or not.
func newTypeInfo() *types.Info {
	return &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Implicits: map[ast.Node]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}, Instances: map[*ast.Ident]types.Instance{}}
}

func isRelativeImport(p string) bool {
	return p == "." || p == ".." || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../")
}

// resolve applies the relative-import rule. Without a base a relative path
// is refused, as the compiler refuses it without -D; with a base it is joined
// and must stay inside the base.
func (m *mapImporter) resolve(p string) (string, error) {
	if !isRelativeImport(p) {
		return p, nil
	}
	if m.base == "" {
		return "", fmt.Errorf("import %q: relative import path requires an import base", p)
	}
	joined := path.Join(m.base, p)
	if joined == ".." || strings.HasPrefix(joined, "../") || !strings.HasPrefix(joined, m.base) {
		return "", fmt.Errorf("import %q: relative import path escapes import base %q", p, m.base)
	}
	return joined, nil
}

func (m *mapImporter) Import(p string) (*types.Package, error) {
	return m.ImportFrom(p, "", 0)
}

func (m *mapImporter) ImportFrom(p, srcDir string, mode types.ImportMode) (*types.Package, error) {
	resolved, err := m.resolve(p)
	if err != nil {
		return nil, err
	}
	if pkg, ok := m.packages[resolved]; ok {
		m.resolutions = append(m.resolutions, Resolution{From: m.from, Import: p, Path: resolved, Origin: "package-map", Name: pkg.Name(), Files: m.files[resolved]})
		return pkg, nil
	}
	if isRelativeImport(p) {
		return nil, fmt.Errorf("import %q: package %q is not in the explicit package map", p, resolved)
	}
	var pkg *types.Package
	if from, ok := m.fallback.(types.ImporterFrom); ok {
		pkg, err = from.ImportFrom(resolved, srcDir, mode)
	} else {
		pkg, err = m.fallback.Import(resolved)
	}
	if err != nil {
		return nil, err
	}
	m.resolutions = append(m.resolutions, Resolution{From: m.from, Import: p, Path: resolved, Origin: "importer", Name: pkg.Name()})
	return pkg, nil
}

// add registers a type-checked package under its explicit path. The path is
// an identity: registering it twice is a caller error, not a merge.
func (m *mapImporter) add(checked *checkedPackage) error {
	spec := checked.spec
	if _, dup := m.packages[spec.Path]; dup {
		return fmt.Errorf("gosource: duplicate package path %q", spec.Path)
	}
	names := make([]string, 0, len(spec.Sources))
	for _, s := range spec.Sources {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	m.packages[spec.Path] = checked.pkg
	m.files[spec.Path] = names
	m.checked[spec.Path] = checked
	m.order = append(m.order, spec.Path)
	return nil
}

// checkDependency parses and type-checks one explicit package in lexical
// filename order and registers it, retaining the ASTs and the checker's Info
// for the link step. Parse and type diagnostics are returned together,
// attributed by file position exactly as the main package's are; nothing is
// converted or executed here.
func (m *mapImporter) checkDependency(fset *token.FileSet, spec PackageSpec, goVersion string) ErrorList {
	if spec.Path == "" {
		return ErrorList{fmt.Errorf("gosource: explicit package with empty import path")}
	}
	if isRelativeImport(spec.Path) {
		return ErrorList{fmt.Errorf("gosource: explicit package path %q must not be relative", spec.Path)}
	}
	if len(spec.Sources) == 0 {
		return ErrorList{fmt.Errorf("gosource: explicit package %q has no source files", spec.Path)}
	}
	sources := append([]Source(nil), spec.Sources...)
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	var diagnostics ErrorList
	var files []*ast.File
	for i, s := range sources {
		if i > 0 && s.Name == sources[i-1].Name {
			return ErrorList{fmt.Errorf("gosource: duplicate file %q in package %q", s.Name, spec.Path)}
		}
		f, err := parser.ParseFile(fset, s.Name, s.Data, parser.ParseComments|parser.AllErrors)
		if err != nil {
			diagnostics = appendDiagnostics(diagnostics, err)
		}
		if f != nil {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return diagnostics
	}
	m.from = spec.Path
	var typeErrors ErrorList
	config := types.Config{Importer: m, GoVersion: goVersion, Error: func(err error) { typeErrors = append(typeErrors, err) }}
	info := newTypeInfo()
	pkg, err := config.Check(spec.Path, fset, files, info)
	diagnostics = append(diagnostics, typeErrors...)
	if err != nil && len(typeErrors) == 0 {
		diagnostics = appendDiagnostics(diagnostics, err)
	}
	if len(diagnostics) > 0 {
		return diagnostics
	}
	if err := m.add(&checkedPackage{spec: spec, sources: sources, files: files, info: info, pkg: pkg}); err != nil {
		return ErrorList{err}
	}
	return nil
}
