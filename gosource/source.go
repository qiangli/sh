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
	"strconv"
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
	// FakeImportC permits import "C" while type checking, matching
	// types.Config.FakeImportC. It does not provide cgo declarations or make
	// cgo source executable by the Bash++ runtime.
	FakeImportC bool
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
	if len(sources) == 0 {
		return nil, fmt.Errorf("gosource: no source files")
	}
	sources = append([]Source(nil), sources...)
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	checker, err := checkerOptionsFor(sources, options)
	if err != nil {
		return nil, err
	}
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
		p.Sources = append(p.Sources, SourceInfo{Name: s.Name, SHA256: fmt.Sprintf("%x", sha256.Sum256(s.Data)), Base: uint(tf.Base() - 1), Size: uint(len(s.Data)), LineDirectives: lineDirectives(tf, f)})
		c.files = append(c.files, f)
		c.sources = append(c.sources, s)
	}
	if len(c.files) == 0 {
		return nil, parseErrors
	}
	parseErrors = append(parseErrors, validateCompilerDirectives(c.fset, c.files, checker)...)
	fallback := options.Importer
	if fallback == nil {
		fallback = importer.Default()
	}
	imp := newMapImporter(options.ImportBase, fallback)
	// Explicit packages are checked first, in order, so a later package sees
	// every earlier one; a failing package stops here with its diagnostics
	// and the program is never checked against a partial map.
	for _, spec := range options.Packages {
		if diagnostics := imp.checkDependency(c.fset, spec, checker); len(diagnostics) > 0 {
			return nil, append(parseErrors, diagnostics...)
		}
	}
	programPath := options.ImportPath
	if programPath == "" {
		programPath = p.Package
	}
	imp.from = programPath
	var typeErrors ErrorList
	config := checker.config(imp, &typeErrors)
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
	c.shadowedBuiltins = shadowedBuiltinTypes(pkg)
	if main, ok := pkg.Scope().Lookup("main").(*types.Func); ok && p.Package == "main" {
		p.Main = main.Name()
	}
	if options.RunMain && p.Main == "" {
		return nil, fmt.Errorf("%s: Go execution requires package main with func main()", sources[0].Name)
	}
	// The link set is every explicit package, in map order, then the
	// program. All are lowered into one flat file, so the hygiene prefix
	// must be free in every file of the set; a mapped package's
	// package-level names are then renamed under it, which keeps the flat
	// namespace collision-free without touching the program's own names.
	var linked []*converter
	mapped := map[string]int{}
	var mappedPkgs []*types.Package
	for i, path := range imp.order {
		checked := imp.checked[path]
		mapped[path] = i
		mappedPkgs = append(mappedPkgs, checked.pkg)
		linked = append(linked, &converter{packagePath: path, fset: c.fset, files: checked.files, sources: checked.sources, info: checked.info, renames: c.renames, shadowedBuiltins: shadowedBuiltinTypes(checked.pkg)})
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
	for _, lc := range linked {
		lc.prefix = c.prefix
		lc.mapped = mapped
		lc.mappedPkgs = mappedPkgs
	}
	mangleLinkedNames(linked, mappedPkgs)
	// Import aliases are shared: every package's imports are hoisted into
	// the one file, so an alias minted by any package names that path for all.
	importAliases := map[string]string{}
	liveImportPaths := liveImports(linked, mapped)
	var lowered []*loweredPackage
	for pi, lc := range linked {
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
		// Only live package bindings need an alias. Blank imports and package
		// bindings with no selector use must not create unused alias imports
		// in the lowered file. The program's own imports keep the file's
		// spelling (C8): the checker already forbids a package-level name in
		// a file block, so in the flat file the only collision left is a
		// name the program's files bind to two different paths, and only the
		// later binding takes an alias. A linked package's imports are
		// hoisted into the same file and always take one.
		bound := map[string]string{}
		for fi, f := range lc.files {
			for ii, spec := range f.Imports {
				var obj types.Object
				if spec.Name != nil {
					obj = lc.info.Defs[spec.Name]
				} else {
					obj = lc.info.Implicits[spec]
				}
				if obj != nil && obj.Name() != "_" && obj.Name() != "." {
					if pkgname, ok := obj.(*types.PkgName); ok {
						path := pkgname.Imported().Path()
						if liveImportPaths[path] {
							if lc != c {
								lc.renames[obj] = fmt.Sprintf("%simport_%d_%d_%d", c.prefix, pi, fi, ii)
							} else if prev, ok := bound[obj.Name()]; ok && prev != path {
								lc.renames[obj] = fmt.Sprintf("%simport_%d_%d", c.prefix, fi, ii)
							} else {
								bound[obj.Name()] = path
							}
						}
						if _, linked := mapped[path]; !linked {
							alias := lc.renames[obj]
							if alias == "" {
								alias = obj.Name()
							}
							importAliases[path] = alias
						}
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
			// These init and main entry calls are synthetic glue with no line
			// in any source file. Borrowing the first declaration's position
			// (p.File.Pos()) stamped a //line on them and mis-attributed -m
			// diagnostics to that unrelated line; leave them unpositioned so
			// the emitter never points a diagnostic back at borrowed source.
			lit := &syntax.Lit{Value: name}
			p.File.Stmts = append(p.File.Stmts, c.stmt(&syntax.BashPPCall{Fun: []*syntax.Lit{lit}}))
		}
	}
	if err := checkLoweredNames(p.File); err != nil {
		return nil, err
	}
	for _, path := range imp.order {
		checked := imp.checked[path]
		p.Packages = append(p.Packages, LinkedPackage{Path: path, Name: checked.pkg.Name(), Files: imp.files[path]})
		// A checked package parsed every source, so files and sources align.
		for i, src := range checked.sources {
			tf := c.fset.File(checked.files[i].FileStart)
			p.Sources = append(p.Sources, SourceInfo{Name: src.Name, SHA256: fmt.Sprintf("%x", sha256.Sum256(src.Data)), Base: uint(tf.Base() - 1), Size: uint(len(src.Data)), LineDirectives: lineDirectives(tf, checked.files[i])})
		}
	}
	c.attachEmbedDirectives(p.File)
	p.File.Sources = append([]syntax.SourceFile(nil), p.Sources...)
	return p, nil
}

func liveImports(linked []*converter, mapped map[string]int) map[string]bool {
	live := map[*types.PkgName]bool{}
	for _, lc := range linked {
		for _, obj := range lc.info.Uses {
			if pkgname, ok := obj.(*types.PkgName); ok {
				live[pkgname] = true
			}
		}
	}
	paths := map[string]bool{}
	for _, lc := range linked {
		for _, f := range lc.files {
			for _, spec := range f.Imports {
				var obj types.Object
				if spec.Name != nil {
					obj = lc.info.Defs[spec.Name]
				} else {
					obj = lc.info.Implicits[spec]
				}
				pkgname, ok := obj.(*types.PkgName)
				if !ok || !live[pkgname] {
					continue
				}
				path := pkgname.Imported().Path()
				if _, linked := mapped[path]; linked {
					continue
				}
				paths[path] = true
			}
		}
	}
	return paths
}

// shadowedBuiltinTypes reports the predeclared type names a package redeclares
// at package scope as something other than a type — `const int = 15`,
// `var error = …`, `func string() {}`. Within such a package the name no
// longer spells the universe type, so the converter must not synthesize a
// `<name>(x)` conversion around a materialized constant.
func shadowedBuiltinTypes(pkg *types.Package) map[string]bool {
	if pkg == nil {
		return nil
	}
	var out map[string]bool
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		if _, ok := types.Universe.Lookup(name).(*types.TypeName); !ok {
			continue
		}
		if _, ok := scope.Lookup(name).(*types.TypeName); ok {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[name] = true
	}
	return out
}

// checkerOptions is the complete policy passed to every types.Config in one
// Load. Keeping it as a value prevents language and cgo-test settings from
// leaking between the program and explicit packages or between Load calls.
type checkerOptions struct {
	goVersion   string
	fakeImportC bool
}

func checkerOptionsFor(sources []Source, options Options) (checkerOptions, error) {
	out := checkerOptions{goVersion: options.GoVersion, fakeImportC: options.FakeImportC}
	// The Go checker corpus places flag-compatible configuration on the first
	// source line (for example "// -lang=go1.13"). Testdir errorcheck recipes
	// use the same flag later on that line. Honor only checker flags, only from
	// the first source, and let explicit API options win.
	line, _, _ := strings.Cut(string(sources[0].Data), "\n")
	if strings.HasPrefix(strings.TrimSpace(line), "//") {
		for _, field := range strings.Fields(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "//"))) {
			if out.goVersion == "" && strings.HasPrefix(field, "-lang=") {
				out.goVersion = strings.TrimPrefix(field, "-lang=")
			}
			if field == "-fakeImportC" && !options.FakeImportC {
				out.fakeImportC = true
			}
		}
	}
	if out.goVersion != "" && !version.IsValid(out.goVersion) {
		return checkerOptions{}, fmt.Errorf("gosource: invalid Go version %q", out.goVersion)
	}
	return out, nil
}

func (o checkerOptions) config(imp types.Importer, diagnostics *ErrorList) types.Config {
	return types.Config{
		Importer:    imp,
		GoVersion:   o.goVersion,
		FakeImportC: o.fakeImportC,
		Error: func(err error) {
			*diagnostics = append(*diagnostics, err)
		},
	}
}

// validateCompilerDirectives covers source checks that the gc compiler runs
// around go/types. The public checker deliberately ignores pragmas, but a
// semantic-only Go source check must not accept a directive that compilation
// will reject.
func validateCompilerDirectives(fset *token.FileSet, files []*ast.File, checker checkerOptions) ErrorList {
	var diagnostics ErrorList
	for _, file := range files {
		haveEmbed := false
		for _, spec := range file.Imports {
			if path, err := strconv.Unquote(spec.Path.Value); err == nil && path == "embed" {
				haveEmbed = true
			}
		}

		var funcs []*ast.FuncDecl
		embedDecl := map[*ast.Comment]*ast.ValueSpec{}
		insideFunc := map[*ast.Comment]bool{}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				funcs = append(funcs, fn)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			gd, ok := node.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				return true
			}
			for _, raw := range gd.Specs {
				value := raw.(*ast.ValueSpec)
				doc := value.Doc
				if doc == nil && len(gd.Specs) == 1 {
					doc = gd.Doc
				}
				if doc == nil {
					continue
				}
				for _, comment := range doc.List {
					embedDecl[comment] = value
					for _, fn := range funcs {
						if fn.Body != nil && fn.Body.Pos() < comment.Slash && comment.End() < fn.Body.End() {
							insideFunc[comment] = true
						}
					}
				}
			}
			return true
		})

		for _, group := range file.Comments {
			for _, comment := range group.List {
				text := strings.TrimPrefix(comment.Text, "//")
				switch {
				case strings.HasPrefix(text, "go:build"):
					if comment.Slash > file.Package {
						diagnostics = append(diagnostics, fmt.Errorf("%s: misplaced compiler directive", fset.Position(comment.Slash)))
					}
				case strings.HasPrefix(text, "go:noinline"):
					allowed := false
					for _, fn := range funcs {
						if fn.Body != nil && fn.Body.Pos() < comment.Slash && comment.End() < fn.Body.End() {
							allowed = false
							break
						}
					}
					if comment.Slash > file.Package && fset.Position(comment.Slash).Column == 1 {
						for _, decl := range file.Decls {
							if comment.Slash < decl.Pos() {
								_, allowed = decl.(*ast.FuncDecl)
								break
							}
						}
					}
					if !allowed {
						diagnostics = append(diagnostics, fmt.Errorf("%s: misplaced compiler directive", fset.Position(comment.Slash)))
					}
				case text == "go:embed" || strings.HasPrefix(text, "go:embed ") || strings.HasPrefix(text, "go:embed\t"):
					value := embedDecl[comment]
					msg := ""
					switch {
					case value == nil:
						msg = "misplaced go:embed directive"
					case !haveEmbed:
						msg = `go:embed only allowed in Go files that import "embed"`
					case len(value.Names) != 1:
						msg = "go:embed cannot apply to multiple vars"
					case len(value.Values) != 0:
						msg = "go:embed cannot apply to var with initializer"
					case value.Type == nil:
						msg = "go:embed cannot apply to var without type"
					case insideFunc[comment]:
						msg = "go:embed cannot apply to var inside func"
					case checker.goVersion != "" && version.Compare(checker.goVersion, "go1.16") < 0:
						msg = fmt.Sprintf("go:embed requires go1.16 or later (-lang was set to %s; check go.mod)", checker.goVersion)
					}
					if msg != "" {
						diagnostics = append(diagnostics, fmt.Errorf("%s: %s", fset.Position(comment.Slash), msg))
					}
				}
			}
		}
	}
	return diagnostics
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

// mangleLinkedNames renames every package-level object of every mapped
// package — types, functions, variables and constants, exported or not — to
// its flat-file spelling, so two linked packages may declare one name and
// the program keeps its own names untouched. Methods are not renamed: they
// dispatch by receiver type, and the receiver's type name is. An embedded
// field is selected by its type's unqualified name, which the runtime
// derives from the field's type spelling, so an embedded field of a renamed
// type takes the same rename; the converter spells the field's uses
// through the field object, and the two agree.
func mangleLinkedNames(linked []*converter, mappedPkgs []*types.Package) {
	if len(mappedPkgs) == 0 {
		return
	}
	// renames is one map shared by every converter of the link set.
	c := linked[0]
	for i, pkg := range mappedPkgs {
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			c.renames[scope.Lookup(name)] = c.mangledName(i, name)
		}
	}
	for _, lc := range linked {
		for _, obj := range lc.info.Defs {
			field, ok := obj.(*types.Var)
			if !ok || !field.Embedded() {
				continue
			}
			typ := field.Type()
			if pointer, ok := typ.(*types.Pointer); ok {
				typ = pointer.Elem()
			}
			if named, ok := typ.(*types.Named); ok {
				if rename := c.renames[named.Obj()]; rename != "" {
					c.renames[field] = rename
				}
			}
		}
	}
}

// checkLoweredNames is the guard behind mangleLinkedNames: the lowered file
// is one flat namespace, so every top-level declaration must have a distinct
// name (a method is keyed by receiver type as well). The checker already
// rejects a package redeclaring a name across its own files and the renames
// keep packages apart, so a duplicate here is a converter defect, reported
// rather than left to alias silently at runtime.
func checkLoweredNames(file *syntax.File) error {
	declared := map[string]bool{}
	for _, stmt := range file.Stmts {
		var name string
		switch d := stmt.Cmd.(type) {
		case *syntax.BashPPFuncDecl:
			name = d.Name.Value
			if d.Receiver != nil && d.Receiver.RecvType != nil {
				name = d.Receiver.RecvType.Value + "." + name
			}
		case *syntax.BashPPDecl:
			name = d.Name.Value
		default:
			continue
		}
		if name == "_" {
			continue
		}
		if declared[name] {
			return fmt.Errorf("gosource: package-level name %s declared twice in the lowered file", name)
		}
		declared[name] = true
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
