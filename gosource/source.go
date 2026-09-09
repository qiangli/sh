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
}
type Program struct {
	File          *syntax.File
	Package       string
	Sources       []SourceInfo
	InitFunctions []string
	Main          string
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
			continue
		}
		if p.Package == "" {
			p.Package = f.Name.Name
		} else if f.Name.Name != p.Package {
			return nil, fmt.Errorf("%s: package %s differs from %s", s.Name, f.Name.Name, p.Package)
		}
		tf := c.fset.File(f.Pos())
		p.Sources = append(p.Sources, SourceInfo{s.Name, fmt.Sprintf("%x", sha256.Sum256(s.Data)), uint(tf.Base() - 1), uint(len(s.Data))})
		c.files = append(c.files, f)
		c.sources = append(c.sources, s)
	}
	if len(parseErrors) > 0 {
		return nil, parseErrors
	}
	imp := options.Importer
	if imp == nil {
		imp = importer.Default()
	}
	var typeErrors ErrorList
	config := types.Config{Importer: imp, Error: func(err error) { typeErrors = append(typeErrors, err) }}
	pkg, err := config.Check(p.Package, c.fset, c.files, c.info)
	if len(typeErrors) > 0 {
		return nil, typeErrors
	}
	if err != nil {
		return nil, err
	}
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
					for i, n := range v.Names {
						node := c.valueDecl(gd, v, n, i)
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
		if len(init.Lhs) != 1 {
			return nil, fmt.Errorf("%s: gosource: tuple package initialization is not implemented", c.fset.Position(init.Rhs.Pos()))
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
