package lower

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func constantAssignStmt(t *testing.T, source string) *syntax.Stmt {
	t.Helper()
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(source), "constants.bpp")
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Stmts) != 1 {
		t.Fatalf("want one statement, got %d", len(file.Stmts))
	}
	return file.Stmts[0]
}

// The measured refusal for `const x int = 1; x=42` is the engine's
// `x: cannot assign to const`, at status 1, with nothing after it running.
// Compile used to reject the same program statically with LOWER-ETYPE
// "cannot assign to x", because the assignment was claimed as a native write
// against a Go constant. It must lower as a shell region instead.
func TestNativeScalarShellAssignmentDeclinesConstantTargets(t *testing.T) {
	e := &emitter{mixedShell: true}
	t.Cleanup(e.releaseLexicalConstants)
	stmt := constantAssignStmt(t, "x=42")
	if !e.nativeScalarShellAssignment(stmt) {
		t.Fatal("a plain integer assignment to a variable is a native write")
	}
	e.noteLexicalConstant("x", "int", "1", true)
	if e.nativeScalarShellAssignment(stmt) {
		t.Fatal("an assignment to a constant was claimed as a native write")
	}
	if !e.dynamicShell(stmt) {
		t.Fatal("an assignment to a constant did not fall through to a shell region")
	}
	// Leaving the constant's scope must release the spelling: a later `var x`
	// in a sibling scope is native storage again.
	e.dropLexicalConstants([]string{"x"})
	if !e.nativeScalarShellAssignment(stmt) {
		t.Fatal("the spelling stayed constant after its scope ended")
	}
}

func TestLexicalConstantTrackingIsPerCompile(t *testing.T) {
	first, second := &emitter{mixedShell: true}, &emitter{mixedShell: true}
	t.Cleanup(first.releaseLexicalConstants)
	t.Cleanup(second.releaseLexicalConstants)
	first.noteLexicalConstant("x", "int", "1", true)
	if !first.lexicalConstantAssigned("x") {
		t.Fatal("the recording emitter lost its own constant")
	}
	if second.lexicalConstantAssigned("x") {
		t.Fatal("one compile's constants leaked into another")
	}
	first.releaseLexicalConstants()
	if first.lexicalConstantAssigned("x") {
		t.Fatal("release did not drop the compile's constants")
	}
}

func TestNoteLexicalConstantDeclIgnoresVariables(t *testing.T) {
	e := &emitter{}
	t.Cleanup(e.releaseLexicalConstants)
	name := &syntax.Lit{Value: "x"}
	e.noteLexicalConstantDecl("var", name, "int", nil)
	if e.lexicalConstantAssigned("x") {
		t.Fatal("a var declaration was recorded as constant")
	}
	e.noteLexicalConstantDecl("const", name, "int", nil)
	if !e.lexicalConstantAssigned("x") {
		t.Fatal("a const declaration was not recorded")
	}
	e.dropLexicalConstants([]string{"x"})
	e.noteLexicalConstant("_", "int", "", false)
	if e.lexicalConstantAssigned("_") {
		t.Fatal("the blank identifier was recorded as a constant")
	}
}

// lexicalConstants is the hook core installs in lexicalStorage. It registers a
// shadow binding by resolved identity and never takes the constant's address.
func TestLexicalConstantsRegistersShadowsByResolvedIdentity(t *testing.T) {
	source := `package generated

import rt "mvdan.cc/sh/v3/lower/shellrt"

type Amount int

func call(program *rt.Program) {
	const x int = 1
	const total Amount = 3
	var y int = 2
	_, _, _ = x, total, y
}

func plain() {
	const hidden int = 9
	_ = hidden
}
`
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "generated.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: bridgeImporter{fallback: newModuleImporter(""), path: DefaultRuntime, cache: map[string]*types.Package{}}}
	if _, err := conf.Check("generated", fs, []*ast.File{file}, info); err != nil {
		t.Fatal(err)
	}
	e := &emitter{prefix: "rt", options: Options{Runtime: DefaultRuntime}}
	edits := e.lexicalConstants(file, fs, info)
	var text strings.Builder
	for _, edit := range edits {
		if edit.start != edit.end {
			t.Fatalf("registration must be an insertion, got %d..%d", edit.start, edit.end)
		}
		text.WriteString(edit.text)
	}
	got := text.String()
	for _, want := range []string{
		`rtrt.RegisterConstant(program.Bindings,"local:` , // resolved identity, not the spelling
		`,"x","int",x,rtrt.KindScalar)`,
		`,"total","Amount",total,rtrt.KindScalar)`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "&x") || strings.Contains(got, "&total") {
		t.Fatalf("the constant's address was taken:\n%s", got)
	}
	if strings.Contains(got, `"y"`) {
		t.Fatalf("a variable was registered as a constant:\n%s", got)
	}
	// A body with no runtime program has no boundary to register against.
	if strings.Contains(got, `"hidden"`) {
		t.Fatalf("a constant outside a program body was registered:\n%s", got)
	}
	if strings.Count(got, "RegisterConstant") != 2 {
		t.Fatalf("want two registrations, got:\n%s", got)
	}
}
