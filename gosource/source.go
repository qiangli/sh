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
	c := &converter{fset: token.NewFileSet(), info: &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Implicits: map[ast.Node]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}, renames: map[types.Object]string{}}
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
	if main, ok := pkg.Scope().Lookup("main").(*types.Func); ok && p.Package == "main" {
		p.Main = main.Name()
	}
	if options.RunMain && p.Main == "" {
		return nil, fmt.Errorf("%s: Go execution requires package main with func main()", sources[0].Name)
	}
	c.prefix = "__gosource_"
	for {
		collision := false
		for _, f := range c.files {
			ast.Inspect(f, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && strings.HasPrefix(id.Name, c.prefix) {
					collision = true
				}
				return true
			})
		}
		if !collision {
			break
		}
		c.prefix += "_"
	}
	c.packagePath = p.Package
	c.importAliases = map[string]string{}
	for id, obj := range c.info.Defs {
		if obj != nil && (id.Name == "nil" || id.Name == "true" || id.Name == "false") {
			c.renames[obj] = fmt.Sprintf("%sbinding_%d", c.prefix, id.Pos())
		}
	}
	// Imports are file scoped. Give each binding a collision-free package alias.
	for fi, f := range c.files {
		for ii, s := range f.Imports {
			var obj types.Object
			if s.Name != nil {
				obj = c.info.Defs[s.Name]
			} else {
				obj = c.info.Implicits[s]
			}
			if obj != nil && obj.Name() != "_" && obj.Name() != "." {
				c.renames[obj] = fmt.Sprintf("%simport_%d_%d", c.prefix, fi, ii)
				if pkgname, ok := obj.(*types.PkgName); ok {
					c.importAliases[pkgname.Imported().Path()] = c.renames[obj]
				}
			}
		}
	}
	var imports, decls, funcs []*syntax.Stmt
	vars := map[*types.Var]*syntax.BashPPDecl{}
	tupleSpecs := map[*types.Var]*ast.ValueSpec{}
	tupleDecls := map[*ast.ValueSpec]*ast.GenDecl{}
	for _, f := range c.files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				fn := c.function(fd)
				if fd.Name.Name == "init" {
					fn.Name.Value = fmt.Sprintf("%sinit_%d", c.prefix, len(p.InitFunctions))
					p.InitFunctions = append(p.InitFunctions, fn.Name.Value)
				}
				funcs = append(funcs, c.stmt(fn))
				continue
			}
			gd := d.(*ast.GenDecl)
			for _, s := range gd.Specs {
				switch v := s.(type) {
				case *ast.ImportSpec:
					imports = append(imports, c.stmt(c.importSpec(gd, v)))
				case *ast.TypeSpec:
					decls = append(decls, c.stmt(c.typeDecl(gd, v)))
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
							decls = append(decls, c.stmt(node))
						}
					}
				}
			}
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	// Function definitions precede all initializers to support forward references.
	p.File.Stmts = append(p.File.Stmts, imports...)
	p.File.Stmts = append(p.File.Stmts, decls...)
	p.File.Stmts = append(p.File.Stmts, funcs...)
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
							p.File.Stmts = append(p.File.Stmts, c.stmt(vars[obj]))
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
			p.File.Stmts = append(p.File.Stmts, c.tupleValueDecls(tupleDecls[spec], spec)...)
			for _, variable := range init.Lhs {
				initialized[variable] = true
			}
			continue
		}
		v := init.Lhs[0]
		p.File.Stmts = append(p.File.Stmts, c.stmt(vars[v]))
		initialized[v] = true
	}
	for v := range vars {
		if !initialized[v] {
			return nil, fmt.Errorf("gosource: missing initialization of %s", v.Name())
		}
	}
	if options.RunMain {
		for _, name := range append(append([]string(nil), p.InitFunctions...), p.Main) {
			pos := p.File.Pos()
			lit := &syntax.Lit{Value: name, ValuePos: pos, ValueEnd: pos}
			p.File.Stmts = append(p.File.Stmts, c.stmt(&syntax.BashPPCall{Fun: []*syntax.Lit{lit}, Lparen: pos, Rparen: pos}))
		}
	}
	if c.err != nil {
		return nil, c.err
	}
	c.attachEmbedDirectives(p.File)
	p.File.Sources = append([]syntax.SourceFile(nil), p.Sources...)
	return p, nil
}
