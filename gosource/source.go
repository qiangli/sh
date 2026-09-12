// Package gosource loads unmodified Go source into the positioned Bash++ AST.
// It uses Go's lexer, parser and type checker; shell parsing is never a fallback.
package gosource

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"go/version"
	"io"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const Version = "gosource-v1"

// ErrorList retains every diagnostic emitted by the Go lexer/parser/checker.
// Error does not collapse scanner errors into an "and N more errors" summary.
type ErrorList []error

func (e ErrorList) Error() string {
	lines := make([]string, len(e))
	for i, err := range e {
		lines[i] = err.Error()
	}
	return strings.Join(lines, "\n")
}
func appendDiagnostics(out ErrorList, err error) ErrorList {
	if list, ok := err.(scanner.ErrorList); ok {
		for _, item := range list {
			out = append(out, item)
		}
	} else if err != nil {
		out = append(out, err)
	}
	return out
}

type Source struct {
	Name string
	Data []byte
}
type SourceInfo = syntax.SourceFile

type Options struct {
	// RunMain appends init and main entry calls. Loading itself never executes code.
	RunMain bool
	// Importer may resolve module dependencies. Nil uses the Go export importer.
	Importer types.Importer
	// GoVersion is passed unchanged to types.Config.GoVersion. Empty preserves
	// the checker default; a nonempty value selects Go language semantics, not
	// an SDK executable. Invalid or newer versions are rejected by the checker.
	GoVersion string
	// Packages are explicitly supplied dependency packages, type-checked in
	// the given order before the program and registered under their Path.
	// They are the policy-free half of Go's import model — an in-memory
	// `-importcfg` — and are consulted before Importer for every import.
	Packages []PackageSpec
	// ImportBase is the compiler's `-D`: a relative import "./x" in any file
	// means ImportBase/x. Empty refuses relative imports. It is never
	// resolved against the filesystem.
	ImportBase string
	// ImportPath is the program package's own import path, used to attribute
	// its resolutions. Empty uses the package name, as before.
	ImportPath string
}
type Program struct {
	File          *syntax.File
	Package       string
	Sources       []SourceInfo
	InitFunctions []string
	Main          string
	// Resolutions records every import the type checker resolved, for the
	// explicit packages and the program alike, in resolution order.
	Resolutions []Resolution
	// Importer is the importer the program was checked with: the explicit
	// package map in front of Options.Importer. Lowering the same program
	// should type-check against it (lower.Options.Importer) so both halves
	// see one map.
	Importer types.Importer
	// Packages lists the explicit packages linked into File, in map order.
	// Each was lowered from its exact sources ahead of the program, so the
	// interpreter runs it without any on-disk lookup; its InitFunctions are
	// included in InitFunctions ahead of the program's own.
	Packages []LinkedPackage
}

// LinkedPackage is one explicit package lowered into Program.File.
type LinkedPackage struct {
	Path  string
	Name  string
	Files []string
}

// SourceAt maps an AST offset to the original source identity and byte offset.
func (p *Program) SourceAt(pos syntax.Pos) (string, uint, bool) {
	for _, s := range p.Sources {
		if pos.Offset() >= s.Base && pos.Offset() <= s.Base+s.Size {
			return s.Name, pos.Offset() - s.Base, true
		}
	}
	return "", 0, false
}
func Parse(r io.Reader, name string, options Options) (*Program, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return Load([]Source{{name, data}}, options)
}

// Load processes a single package in lexical filename order, matching the Go
// toolchain. Source bytes are neither modified nor executed by the native toolchain.
func Load(sources []Source, options Options) (*Program, error) {
	if options.GoVersion != "" && !version.IsValid(options.GoVersion) {
		return nil, fmt.Errorf("gosource: invalid Go version %q", options.GoVersion)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("gosource: no source files")
	}
	sources = append([]Source(nil), sources...)
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	c := &converter{fset: token.NewFileSet(), info: newTypeInfo(), renames: map[types.Object]string{}}
	p := &Program{File: &syntax.File{Name: sources[0].Name, GoSource: true}}
	var parseErrors ErrorList
	for i, s := range sources {
		if i > 0 && s.Name == sources[i-1].Name {
			return nil, fmt.Errorf("gosource: duplicate file %q", s.Name)
		}
		f, err := parser.ParseFile(c.fset, s.Name, s.Data, parser.ParseComments|parser.AllErrors)
		if err != nil {
			parseErrors = appendDiagnostics(parseErrors, err)
		}
		// A recovered file still contains declarations and bodies for the Go
		// checker. Keep it for diagnostics only; no errored AST is converted.
		if f == nil {
			continue
		}
		if p.Package == "" {
			p.Package = f.Name.Name
		}
		tf := c.fset.File(f.FileStart)
		p.Sources = append(p.Sources, SourceInfo{s.Name, fmt.Sprintf("%x", sha256.Sum256(s.Data)), uint(tf.Base() - 1), uint(len(s.Data))})
		c.files = append(c.files, f)
		c.sources = append(c.sources, s)
	}
	if len(c.files) == 0 {
		return nil, parseErrors
	}
	fallback := options.Importer
	if fallback == nil {
		fallback = importer.Default()
	}
	imp := newMapImporter(options.ImportBase, fallback)
	// Explicit packages are checked first, in order, so a later package sees
	// every earlier one; a failing package stops here with its diagnostics
	// and the program is never checked against a partial map.
	for _, spec := range options.Packages {
		if diagnostics := imp.checkDependency(c.fset, spec, options.GoVersion); len(diagnostics) > 0 {
			return nil, append(parseErrors, diagnostics...)
		}
	}
	programPath := options.ImportPath
	if programPath == "" {
		programPath = p.Package
	}
	imp.from = programPath
	var typeErrors ErrorList
	config := types.Config{Importer: imp, GoVersion: options.GoVersion, Error: func(err error) { typeErrors = append(typeErrors, err) }}
	pkg, err := config.Check(programPath, c.fset, c.files, c.info)
	// Match the native checker test flow: parser diagnostics first, followed
	// by semantic diagnostics from every recoverable file. Check's returned
	// first error is already reported through Error; do not duplicate it.
	diagnostics := append(parseErrors, typeErrors...)
	if err != nil && len(typeErrors) == 0 {
		diagnostics = appendDiagnostics(diagnostics, err)
	}
	if len(diagnostics) > 0 {
		return nil, diagnostics
	}
	p.Resolutions = imp.resolutions
	p.Importer = imp
	if main, ok := pkg.Scope().Lookup("main").(*types.Func); ok && p.Package == "main" {
		p.Main = main.Name()
	}
	if options.RunMain && p.Main == "" {
		return nil, fmt.Errorf("%s: Go execution requires package main with func main()", sources[0].Name)
	}
	// The link set is every explicit package, in map order, then the
	// program. All are lowered into one flat file, so the hygiene prefix
	// must be free in every file of the set and the package-level names
	// must be distinct across it.
	var linked []*converter
	mapped := map[string]bool{}
	for _, path := range imp.order {
		checked := imp.checked[path]
		mapped[path] = true
		linked = append(linked, &converter{packagePath: path, fset: c.fset, files: checked.files, sources: checked.sources, info: checked.info, renames: c.renames})
	}
	c.packagePath = programPath
	linked = append(linked, c)
	c.prefix = "__gosource_"
	for {
		collision := false
		for _, lc := range linked {
			for _, f := range lc.files {
				ast.Inspect(f, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && strings.HasPrefix(id.Name, c.prefix) {
						collision = true
					}
					return true
				})
			}
		}
		if !collision {
			break
		}
		c.prefix += "_"
	}
	if err := checkLinkedNames(imp, pkg, programPath); err != nil {
		return nil, err
	}
	// Import aliases are shared: every package's imports are hoisted into
	// the one file, so an alias minted by any package names that path for all.
	importAliases := map[string]string{}
	var lowered []*loweredPackage
	for pi, lc := range linked {
		lc.prefix = c.prefix
		lc.mapped = mapped
		lc.importAliases = importAliases
		lc.resolveImport = imp.resolve
		if lc != c {
			if err := refuseEmbedDirectives(lc); err != nil {
				return nil, err
			}
		}
		for id, obj := range lc.info.Defs {
			if obj != nil && (id.Name == "nil" || id.Name == "true" || id.Name == "false") {
				lc.renames[obj] = fmt.Sprintf("%sbinding_%d", c.prefix, id.Pos())
			}
		}
		// Imports are file scoped. Give each binding a collision-free package
		// alias; a mapped package's aliases carry its map index as well.
		for fi, f := range lc.files {
			for ii, spec := range f.Imports {
				var obj types.Object
				if spec.Name != nil {
					obj = lc.info.Defs[spec.Name]
				} else {
					obj = lc.info.Implicits[spec]
				}
				if obj != nil && obj.Name() != "_" && obj.Name() != "." {
					if lc == c {
						lc.renames[obj] = fmt.Sprintf("%simport_%d_%d", c.prefix, fi, ii)
					} else {
						lc.renames[obj] = fmt.Sprintf("%simport_%d_%d_%d", c.prefix, pi, fi, ii)
					}
					if pkgname, ok := obj.(*types.PkgName); ok && !mapped[pkgname.Imported().Path()] {
						importAliases[pkgname.Imported().Path()] = lc.renames[obj]
					}
				}
			}
		}
		lp, err := lc.lowerPackage(len(p.InitFunctions))
		if err != nil {
			return nil, err
		}
		p.InitFunctions = append(p.InitFunctions, lp.initFunctions...)
		lowered = append(lowered, lp)
	}
	// Imports first: the interpreter starts the native dependency bridge once,
	// at the first statement that is not an import.
	for _, lp := range lowered {
		p.File.Stmts = append(p.File.Stmts, lp.imports...)
	}
	// Then each package in link order, a dependency completely before its
	// importer: function definitions precede all initializers to support
	// forward references, and a mapped package's init calls run before the
	// next package's initializers, as Go orders them.
	for i, lp := range lowered {
		p.File.Stmts = append(p.File.Stmts, lp.decls...)
		p.File.Stmts = append(p.File.Stmts, lp.funcs...)
		p.File.Stmts = append(p.File.Stmts, lp.inits...)
		if !options.RunMain {
			continue
		}
		calls := lp.initFunctions
		if i == len(lowered)-1 {
			calls = append(append([]string(nil), calls...), p.Main)
		}
		for _, name := range calls {
			pos := p.File.Pos()
			lit := &syntax.Lit{Value: name, ValuePos: pos, ValueEnd: pos}
			p.File.Stmts = append(p.File.Stmts, c.stmt(&syntax.BashPPCall{Fun: []*syntax.Lit{lit}, Lparen: pos, Rparen: pos}))
		}
	}
	for _, path := range imp.order {
		checked := imp.checked[path]
		p.Packages = append(p.Packages, LinkedPackage{Path: path, Name: checked.pkg.Name(), Files: imp.files[path]})
		// A checked package parsed every source, so files and sources align.
		for i, src := range checked.sources {
			tf := c.fset.File(checked.files[i].FileStart)
			p.Sources = append(p.Sources, SourceInfo{Name: src.Name, SHA256: fmt.Sprintf("%x", sha256.Sum256(src.Data)), Base: uint(tf.Base() - 1), Size: uint(len(src.Data))})
		}
	}
	c.attachEmbedDirectives(p.File)
	p.File.Sources = append([]syntax.SourceFile(nil), p.Sources...)
	return p, nil
}

// loweredPackage is one package's lowered top-level statements, grouped so
// Load can hoist every package's imports ahead of the first declaration and
// emit the rest per package in link order.
type loweredPackage struct {
	imports, decls, funcs []*syntax.Stmt
	// inits holds the zero-initialized globals followed by the checker's
	// InitOrder initializers.
	inits         []*syntax.Stmt
	initFunctions []string
}

// lowerPackage converts the converter's files into grouped statements. The
// init functions are numbered from initBase so names stay unique across the
// link set.
func (c *converter) lowerPackage(initBase int) (*loweredPackage, error) {
	out := &loweredPackage{}
	vars := map[*types.Var]*syntax.BashPPDecl{}
	tupleSpecs := map[*types.Var]*ast.ValueSpec{}
	tupleDecls := map[*ast.ValueSpec]*ast.GenDecl{}
	for _, f := range c.files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				fn := c.function(fd)
				if fd.Name.Name == "init" && fd.Recv == nil {
					fn.Name.Value = fmt.Sprintf("%sinit_%d", c.prefix, initBase+len(out.initFunctions))
					out.initFunctions = append(out.initFunctions, fn.Name.Value)
				}
				out.funcs = append(out.funcs, c.stmt(fn))
				continue
			}
			gd := d.(*ast.GenDecl)
			for _, s := range gd.Specs {
				switch v := s.(type) {
				case *ast.ImportSpec:
					// An import satisfied by the explicit package map is
					// linked, not imported: nothing reaches the runtime.
					if imp := c.importSpec(gd, v); imp != nil {
						out.imports = append(out.imports, c.stmt(imp))
					}
				case *ast.TypeSpec:
					out.decls = append(out.decls, c.stmt(c.typeDecl(gd, v)))
				case *ast.ValueSpec:
					valueSpec := v
					if gd.Tok == token.VAR && len(v.Values) == 1 && len(v.Names) > 1 {
						zero := *v
						zero.Values = nil
						valueSpec = &zero
						tupleDecls[v] = gd
						for _, n := range v.Names {
							tupleSpecs[c.info.Defs[n].(*types.Var)] = v
						}
					}
					for i, n := range v.Names {
						node := c.valueDecl(gd, valueSpec, n, i)
						if gd.Tok == token.VAR {
							vars[c.info.Defs[n].(*types.Var)] = node
						} else {
							out.decls = append(out.decls, c.stmt(node))
						}
					}
				}
			}
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	initialized := map[*types.Var]bool{}
	// Zero-initialized globals are available to initializer functions.
	for _, f := range c.files {
		for _, d := range f.Decls {
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				for _, s := range gd.Specs {
					v := s.(*ast.ValueSpec)
					if len(v.Values) == 0 {
						for _, n := range v.Names {
							obj := c.info.Defs[n].(*types.Var)
							out.inits = append(out.inits, c.stmt(vars[obj]))
							initialized[obj] = true
						}
					}
				}
			}
		}
	}
	for _, init := range c.info.InitOrder {
		if len(init.Lhs) > 1 {
			spec := tupleSpecs[init.Lhs[0]]
			if spec == nil {
				return nil, fmt.Errorf("%s: gosource: missing tuple initializer", c.fset.Position(init.Rhs.Pos()))
			}
			out.inits = append(out.inits, c.tupleValueDecls(tupleDecls[spec], spec)...)
			for _, variable := range init.Lhs {
				initialized[variable] = true
			}
			continue
		}
		v := init.Lhs[0]
		out.inits = append(out.inits, c.stmt(vars[v]))
		initialized[v] = true
	}
	for v := range vars {
		if !initialized[v] {
			return nil, fmt.Errorf("gosource: missing initialization of %s", v.Name())
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	return out, nil
}

// checkLinkedNames refuses a link set whose packages declare one package-level
// name twice. The lowered file is one flat namespace and the converter renames
// only import bindings and predeclared-identifier shadows, never declarations,
// so a shared name would silently alias rather than shadow.
func checkLinkedNames(imp *mapImporter, program *types.Package, programPath string) error {
	type member struct {
		path string
		pkg  *types.Package
	}
	var set []member
	for _, path := range imp.order {
		set = append(set, member{path, imp.checked[path].pkg})
	}
	set = append(set, member{programPath, program})
	declared := map[string]string{}
	for _, m := range set {
		for _, name := range m.pkg.Scope().Names() {
			if name == "_" {
				continue
			}
			if first, dup := declared[name]; dup {
				return fmt.Errorf("gosource: package-level name %s declared by both %s and %s; execution against the explicit package map requires distinct names", name, first, m.path)
			}
			declared[name] = m.path
		}
	}
	return nil
}

// refuseEmbedDirectives rejects go:embed inside a mapped package: embed
// directives are attached for the program's files only and the runtime's
// embed request assumes the program's single source root.
func refuseEmbedDirectives(c *converter) error {
	for _, f := range c.files {
		for _, group := range f.Comments {
			for _, comment := range group.List {
				if strings.HasPrefix(comment.Text, "//go:embed ") || strings.HasPrefix(comment.Text, "//go:embed\t") || comment.Text == "//go:embed" {
					return fmt.Errorf("%s: gosource: go:embed in mapped package %q is not supported by the explicit package map", c.fset.Position(comment.Slash), c.packagePath)
				}
			}
		}
	}
	return nil
}
