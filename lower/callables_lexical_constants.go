// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package lower

import (
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"
)

// A source `const` lowers to a real Go `const`. That is the whole point: the
// declaration is not storage, so no native path can write it and no missed
// check can quietly make it writable. Two things still have to happen for the
// shell boundary.
//
// First, `x=42` where x is constant must stop being lowered as a native Go
// assignment. It is not one — Go rejects it, and `Compile` was reporting
// `cannot assign to x (neither addressable nor a map index expression)` for a
// program the engine runs. The statement is an ordinary shell assignment whose
// outcome the declaration policy decides, so `nativeScalarShellAssignment`
// must decline to claim it. See lexicalConstantAssigned below.
//
// Second, the constant needs a shadow registration at the boundary so a shell
// region can read `$x` and so the backend can be told which identity is
// constant. That is lexicalConstants, a []lexicalEdit producer shaped exactly
// like lexicalLocals so core can install it with one line.

// lexicalConstantState is per-compile constant tracking. The emitter type
// lives in compile.go, which this slice does not own, so the set is keyed by
// the emitter that is lowering rather than stored as a field on it. Entries
// are dropped when the compile finishes; see releaseLexicalConstants.
var lexicalConstantState sync.Map // *emitter -> *lexicalConstants

type lexicalConstant struct {
	sourceType string
	text       string
	hasText    bool
}

type lexicalConstants struct {
	mu    sync.Mutex
	names map[string]lexicalConstant
}

func (e *emitter) lexicalConstantSet() *lexicalConstants {
	if set, ok := lexicalConstantState.Load(e); ok {
		return set.(*lexicalConstants)
	}
	set, _ := lexicalConstantState.LoadOrStore(e, &lexicalConstants{names: map[string]lexicalConstant{}})
	return set.(*lexicalConstants)
}

// noteLexicalConstant records that a source name was introduced by `const`.
// Core calls it from the `const` arm of compile.go's declaration lowering, for
// both BashPPDecl and BashPPConstGroup. sourceType may be empty when the
// declaration has no explicit type; text is the retained literal spelling when
// there is one, which is what the boundary projects.
func (e *emitter) noteLexicalConstant(name, sourceType, text string, hasText bool) {
	if name == "" || name == "_" {
		return
	}
	set := e.lexicalConstantSet()
	set.mu.Lock()
	set.names[name] = lexicalConstant{sourceType: sourceType, text: text, hasText: hasText}
	set.mu.Unlock()
}

// noteLexicalConstantDecl is the convenience form for a parsed declaration.
// A `const` group member and a single `const x T = v` reach it the same way.
func (e *emitter) noteLexicalConstantDecl(kw string, name *syntax.Lit, typ string, value *syntax.Word) {
	if kw != "const" || name == nil {
		return
	}
	text, hasText := "", false
	if value != nil && len(value.Parts) == 1 {
		if lit, ok := value.Parts[0].(*syntax.Lit); ok {
			text, hasText = lit.Value, true
		}
	}
	e.noteLexicalConstant(name.Value, typ, text, hasText)
}

// dropLexicalConstants forgets names as their scope ends, so an inner `var` of
// the same spelling in a later sibling scope is not mistaken for the constant
// that used to hold that name. Core calls it wherever the emitter pops a scope.
func (e *emitter) dropLexicalConstants(names []string) {
	set := e.lexicalConstantSet()
	set.mu.Lock()
	for _, name := range names {
		delete(set.names, name)
	}
	set.mu.Unlock()
}

// releaseLexicalConstants drops this compile's tracking. Core calls it when
// compilePass returns; it is safe to call more than once.
func (e *emitter) releaseLexicalConstants() { lexicalConstantState.Delete(e) }

// lexicalConstantAssigned reports that an ordinary shell assignment targets a
// constant. It is consumed by nativeScalarShellAssignment, which must then
// decline the statement so it lowers as a shell region rather than as a native
// Go assignment the type checker will reject.
func (e *emitter) lexicalConstantAssigned(name string) bool {
	set, ok := lexicalConstantState.Load(e)
	if !ok {
		return false
	}
	constants := set.(*lexicalConstants)
	constants.mu.Lock()
	defer constants.mu.Unlock()
	_, constant := constants.names[name]
	return constant
}

// lexicalConstants emits the shadow registration for every source constant
// declared inside a callable body. It is the postpass counterpart of
// lexicalLocals and produces nothing else, so core installs it in
// lexicalStorage with a single line:
//
//	edits = append(edits, e.lexicalConstants(file, fs, info)...)
//
// Identity keys match lexicalLocals exactly — "local:<offset>:<name>" — so a
// constant and a variable can never collide and a shadowed constant keeps its
// own binding.
func (e *emitter) lexicalConstants(file *ast.File, fs *token.FileSet, info *types.Info) []lexicalEdit {
	var edits []lexicalEdit
	sourceName := func(id *ast.Ident) bool {
		return id != nil && id.Name != "_" && !strings.HasPrefix(id.Name, e.prefix) && info.Defs[id] != nil
	}
	registration := func(ids []*ast.Ident, program string) string {
		text := ""
		for _, id := range ids {
			if !sourceName(id) {
				continue
			}
			object, ok := info.Defs[id].(*types.Const)
			if !ok {
				continue
			}
			key := "local:" + strconv.Itoa(fs.Position(id.Pos()).Offset) + ":" + id.Name
			// The value is passed, never its address: `&x` does not compile
			// for a constant, and rewriting the declaration into a var to
			// obtain one would be the demotion this file exists to avoid.
			text += e.prefix + "rt.MustReadonly(" + e.prefix + "rt.RegisterConstant(" +
				program + ".Bindings," + strconv.Quote(key) + "," + strconv.Quote(id.Name) + "," +
				strconv.Quote(constantSourceType(object)) + "," + id.Name + "," +
				e.prefix + "rt." + constantKind(object) + "))\n"
		}
		return text
	}
	var walk func(ast.Node, string)
	walk = func(node ast.Node, program string) {
		if node == nil {
			return
		}
		switch n := node.(type) {
		case *ast.FuncDecl:
			walk(n.Body, functionProgram(n.Type, program))
			return
		case *ast.FuncLit:
			walk(n.Body, functionProgram(n.Type, program))
			return
		case *ast.DeclStmt:
			if program != "" {
				if gen, ok := n.Decl.(*ast.GenDecl); ok && gen.Tok == token.CONST {
					var ids []*ast.Ident
					for _, spec := range gen.Specs {
						value, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						ids = append(ids, value.Names...)
					}
					if text := registration(ids, program); text != "" {
						offset := fs.Position(n.End()).Offset
						edits = append(edits, lexicalEdit{offset, offset, "\n" + text})
					}
				}
			}
		}
		ast.Inspect(node, func(child ast.Node) bool {
			if child == nil {
				return false
			}
			if child == node {
				return true
			}
			walk(child, program)
			return false
		})
	}
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		program := functionProgram(function.Type, "")
		if program == "" {
			continue
		}
		for _, stmt := range function.Body.List {
			walk(stmt, program)
		}
	}
	return edits
}

// functionProgram finds the runtime program parameter a body registers
// against. A body without one has no boundary to register with.
func functionProgram(signature *ast.FuncType, inherited string) string {
	if signature == nil || signature.Params == nil {
		return inherited
	}
	for _, field := range signature.Params.List {
		ptr, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		selector, ok := ptr.X.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Program" || len(field.Names) != 1 {
			continue
		}
		return field.Names[0].Name
	}
	return inherited
}

// constantSourceType reports the source type spelling the boundary should
// report, which is the declared named type rather than Go's rendering of its
// underlying type: `type Amount int` must diagnose as Amount.
func constantSourceType(object *types.Const) string {
	if named, ok := object.Type().(*types.Named); ok {
		return named.Obj().Name()
	}
	return object.Type().String()
}

func constantKind(object *types.Const) string {
	switch object.Type().Underlying().(type) {
	case *types.Interface:
		return "KindInterface"
	}
	return "KindScalar"
}
