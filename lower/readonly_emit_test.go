package lower

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// readonlyContextNames are ordinary local expressions in the generated program.
// Nothing here is a package-level variable, a thread local or a goroutine-id
// lookup; the boundary function is passed in exactly like the state.
var readonlyContextNames = ReadonlyContext{State: "__test_state", Abort: "__test_abort"}

func newReadonlyEmitter() *emitter {
	return &emitter{
		prefix:          "__test_",
		scopes:          []map[string]bool{{}},
		funcs:           map[string]bool{},
		imports:         map[string]string{},
		dotNames:        map[string]bool{},
		typeNames:       map[string]bool{},
		globals:         map[string]bool{},
		functionGlobals: map[string]bool{},
	}
}

func readonlySource(node syntax.Node) string {
	var out strings.Builder
	_ = syntax.NewPrinter().Print(&out, node)
	return out.String()
}

// readonlyEmit is the harness dispatcher. It is deliberately minimal: it wires
// the guard helpers to the nodes this slice owns and delegates everything else
// to the ordinary emitter. It is not the compiler's statement dispatcher and
// certifies nothing about it.
type readonlyEmit struct {
	e     *emitter
	kinds []string
	used  int
	decls strings.Builder
	t     *testing.T
}

func (h *readonlyEmit) nextKind() string {
	if h.used >= len(h.kinds) {
		h.t.Fatalf("fixture supplies %d mutation kinds, dispatcher needs more", len(h.kinds))
	}
	kind := h.kinds[h.used]
	h.used++
	return kind
}

func (h *readonlyEmit) stmts(stmts []*syntax.Stmt) (string, error) {
	var out strings.Builder
	for _, stmt := range stmts {
		var text string
		var err error
		switch node := stmt.Cmd.(type) {
		case *syntax.BashPPImport:
			err = h.e.importDecl(node)
		case *syntax.BashPPFuncDecl:
			err = h.function(node)
		case *syntax.DeclClause:
			text, err = h.e.readonlyDeclare(node, readonlyContextNames)
		case *syntax.BashPPAssign:
			text, err = h.assign(node)
		case *syntax.Subshell:
			// A subshell is lexical scaffolding here: this fixture's guard fires
			// before any mutation, so the block form is observationally identical.
			// Real subshell lowering belongs to the compiler owner.
			var inner string
			inner, err = h.stmts(node.Stmts)
			text = "{\n" + inner + "}"
		case *syntax.BashPPShortDecl:
			text, err = h.shortDecl(node)
		case *syntax.BashPPCall:
			if len(node.Fun) == 1 && h.e.funcs[node.Fun[0].Value] && len(node.Args) == 0 {
				// The state is threaded as an ordinary parameter, exactly as the
				// contract requires; nothing is reachable through a global.
				text = h.e.goName(node.Fun[0].Value) + "(" + readonlyContextNames.State + ")"
				break
			}
			if readonlyGuardedBuiltin(node) {
				var setup, call string
				setup, call, err = h.builtin(node)
				text = setup + call
				break
			}
			text, err = h.e.command(stmt.Cmd)
		default:
			text, err = h.e.command(stmt.Cmd)
		}
		if err != nil {
			return "", err
		}
		if text != "" {
			out.WriteString(text + "\n")
		}
	}
	return out.String(), nil
}

// function keeps the parameterless declarations these fixtures use. Full
// callable lowering is the compiler owner's; the harness only needs a body
// scope so `make` and the guards can be exercised where the parser accepts them.
func (h *readonlyEmit) function(node *syntax.BashPPFuncDecl) error {
	if node.Name == nil || node.Body == nil || len(node.Params) > 0 || len(node.Results) > 0 {
		return errors.New("harness supports parameterless function declarations only")
	}
	h.e.funcs[node.Name.Value] = true
	h.e.push()
	body, err := h.stmts(node.Body.Stmts)
	h.e.pop()
	if err != nil {
		return err
	}
	h.decls.WriteString("func " + h.e.goName(node.Name.Value) + "(" + readonlyContextNames.State +
		" *__test_rt.ReadonlyState) {\n" + body + "}\n")
	return nil
}

func (h *readonlyEmit) assign(node *syntax.BashPPAssign) (string, error) {
	if node.TargetExpr != nil {
		value, err := h.e.expr(node.ValueExpr)
		if err != nil {
			return "", err
		}
		return h.e.readonlyMutation(node.TargetExpr, readonlyContextNames, ReadonlyMutation{Kind: h.nextKind(), Value: value})
	}
	// Whole-binding rebinding still arrives as words. The compiler owner must
	// supply the emitted value; the harness re-prints the positioned word, which
	// is already valid Go for these fixtures.
	name := readonlySource(node.Target)
	return h.e.readonlyRebind(node, readonlyContextNames, name, readonlySource(node.Value))
}

func (h *readonlyEmit) shortDecl(node *syntax.BashPPShortDecl) (string, error) {
	if node.Call != nil && (len(node.Call.Fun) > 1 || readonlyGuardedBuiltin(node.Call)) {
		var rhs, setup string
		var err error
		if readonlyGuardedBuiltin(node.Call) {
			setup, rhs, err = h.builtin(node.Call)
		} else {
			// Imported constructors are the compiler's projection work; the
			// harness emits the ordinary native call so the fixture can run.
			rhs, err = h.e.call(node.Call)
		}
		if err != nil {
			return "", err
		}
		declared := names(node.Lhs)
		operator := " := "
		if strings.Trim(strings.Join(declared, ""), "_") == "" {
			operator = " = "
		}
		for _, name := range declared {
			h.e.bind(name)
		}
		return setup + strings.Join(declared, ", ") + operator + rhs + h.e.unused(declared), nil
	}
	return h.e.command(node)
}

func readonlyGuardedBuiltin(call *syntax.BashPPCall) bool {
	if call == nil || len(call.Fun) != 1 {
		return false
	}
	switch call.Fun[0].Value {
	case "append", "copy", "delete", "clear":
		return true
	}
	return false
}

func (h *readonlyEmit) builtin(call *syntax.BashPPCall) (string, string, error) {
	if len(call.Args) == 0 {
		return "", "", errors.New("collection builtin without a target")
	}
	target, err := h.e.argument(call.Args[0])
	if err != nil {
		return "", "", err
	}
	var rest []string
	for _, arg := range call.Args[1:] {
		value, err := h.e.argument(arg)
		if err != nil {
			return "", "", err
		}
		rest = append(rest, value)
	}
	source := readonlySource(call.Args[0])
	return h.e.readonlyBuiltin(call, readonlyContextNames, ReadonlyBuiltin{
		Name: call.Fun[0].Value, Root: source, Target: target, Path: source, Args: rest,
		Spread: call.Ellipsis.IsValid(),
	})
}

// readonlyProgram assembles the ordinary Go artifact around the emitted body.
// The helpers themselves never build a program wrapper; this is the test's own
// stand-in for the compiler's boundary.
func readonlyProgram(e *emitter, declarations, body string) string {
	return `package main

import __test_rt "mvdan.cc/sh/v3/lower/shellrt"
import __test_fmt "fmt"
import __test_os "os"
` + e.importLines() + `
func __test_abort(err error) {
	__test_fmt.Fprintln(__test_rt.Stderr, err)
	status := 1
	var exit interface{ ExitStatus() int }
	if __test_errors_As(err, &exit) {
		status = exit.ExitStatus()
	}
	__test_os.Exit(status)
}

func __test_errors_As(err error, target *interface{ ExitStatus() int }) bool {
	for err != nil {
		if value, ok := err.(interface{ ExitStatus() int }); ok {
			*target = value
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

` + declarations + `
func main() {
	__test_state := &__test_rt.ReadonlyState{}
	_ = __test_state
` + body + "}\n"
}

type readonlyRun struct {
	stdout, stderr string
	status         int
}

func readonlyBuildAndRun(t *testing.T, ctx context.Context, program string) readonlyRun {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "generated.go")
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	_, testFile, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(testFile))
	module := "module readonlyartifact\ngo " + strings.TrimPrefix(runtime.Version(), "go") +
		"\nrequire mvdan.cc/sh/v3 v3.0.0\nreplace mvdan.cc/sh/v3 => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "program")
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, "generated.go")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build:%v\n%s\n%s", err, out, program)
	}
	// The compiled artifact must be an ordinary Go binary: no source, no shell
	// and no Go tool are reachable while it runs.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary)
	command.Dir = t.TempDir()
	command.Env = []string{"PATH="}
	var out, errs bytes.Buffer
	command.Stdout = &out
	command.Stderr = &errs
	err := command.Run()
	status := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("artifact:%v\n%s", err, errs.String())
		}
		status = exit.ExitCode()
	}
	return readonlyRun{stdout: out.String(), stderr: errs.String(), status: status}
}

func readonlyInterpret(t *testing.T, ctx context.Context, file *syntax.File) readonlyRun {
	t.Helper()
	var out, errs bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &out, &errs),
		interp.Dir(t.TempDir()), interp.Env(expand.ListEnviron("PATH=")))
	if err != nil {
		t.Fatal(err)
	}
	status := 0
	if err := runner.Run(ctx, file); err != nil {
		var exit interp.ExitStatus
		if !errors.As(err, &exit) {
			t.Fatalf("interpreter:%v\n%s", err, errs.String())
		}
		status = int(exit)
	}
	return readonlyRun{stdout: out.String(), stderr: errs.String(), status: status}
}

// TestReadonlyEmitterNegativeArtifactParity covers the seven public readonly
// negatives. Each one must compile as ordinary Go and fail at runtime with the
// interpreter's diagnostic and exit status, not at compile time.
func TestReadonlyEmitterNegativeArtifactParity(t *testing.T) {
	fixtures := []struct {
		name, src string
		kinds     []string
	}{
		{"root", `cfg := map[string]int{"port": 80}
readonly cfg
cfg = map[string]int{"port": 443}
`, nil},
		{"map", `cfg := map[string]map[string]int{"nested": {"port": 80}}
readonly cfg
cfg["nested"]["port"] = 443
`, []string{"map"}},
		{"slice", `cfg := map[string][]int{"ports": {80, 443}}
readonly cfg
cfg["ports"][0] = 8080
`, []string{"slice"}},
		{"struct", `type Config struct { Name string }
cfg := Config{Name: "prod"}
readonly cfg
cfg.Name = "dev"
`, []string{"field"}},
		{"alias", `cfg := map[string][]int{"ports": {80, 443}}
alias := cfg
readonly cfg
alias["ports"][0] = 8080
`, []string{"slice"}},
		{"subshell", `cfg := map[string][]int{"ports": {80, 443}}
readonly cfg
(
 cfg["ports"][0] = 8080
)
`, []string{"slice"}},
		{"imported", `import "net/url"
endpoint, _ := url.Parse("https://example.test/original")
readonly endpoint
endpoint.Host = "changed.test"
`, []string{"field"}},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(fixture.src), "readonly.bpp")
			if err != nil {
				t.Fatal(err)
			}
			e := newReadonlyEmitter()
			harness := &readonlyEmit{e: e, kinds: fixture.kinds, t: t}
			body, err := harness.stmts(file.Stmts)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			compiled := readonlyBuildAndRun(t, ctx, readonlyProgram(e, harness.decls.String(), body))
			interpreted := readonlyInterpret(t, ctx, file)
			if interpreted.status != 2 {
				t.Fatalf("interpreter status=%d stderr=%q", interpreted.status, interpreted.stderr)
			}
			if compiled != interpreted {
				t.Fatalf("compiled=%+v interpreted=%+v", compiled, interpreted)
			}
		})
	}
}

// TestReadonlyEmitterBuiltinArtifacts certifies that the builtin guards compile
// and abort at runtime with the readonly diagnostic code and status. The
// runtime cannot yet reproduce the interpreter's "through <builtin>" sentence,
// so wording is explicitly not compared here; see the doc's API gap.
func TestReadonlyEmitterBuiltinArtifacts(t *testing.T) {
	fixtures := []struct {
		name, src string
		guarded   bool
	}{
		{"clear", "s := []int{1}\nreadonly s\nclear(s)\n", true},
		{"append in place", "func main() {\n s := make([]int, 0, 1)\n readonly s\n _ := append(s, 1)\n}\nmain()\n", true},
		{"delete", "m := map[string]int{\"a\": 1}\nreadonly m\ndelete(m, \"a\")\n", true},
		{"copy", "s := []int{1, 2}\nreadonly s\ncopy(s, s)\n", true},
		// A growing append allocates, so neither implementation reports a
		// mutation of the readonly backing array.
		{"append grows", "s := []int{1}\nreadonly s\nt := append(s, 2)\nprintln(t[1])\n", false},
	}
	const code = "BASHPP-EREADONLY-MUTATION:"
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(fixture.src), "readonly.bpp")
			if err != nil {
				t.Fatal(err)
			}
			e := newReadonlyEmitter()
			harness := &readonlyEmit{e: e, t: t}
			body, err := harness.stmts(file.Stmts)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			compiled := readonlyBuildAndRun(t, ctx, readonlyProgram(e, harness.decls.String(), body))
			interpreted := readonlyInterpret(t, ctx, file)
			if compiled.status != interpreted.status || compiled.stdout != interpreted.stdout {
				t.Fatalf("compiled=%+v interpreted=%+v", compiled, interpreted)
			}
			if !fixture.guarded {
				// An unguarded builtin must agree completely, wording included.
				if compiled != interpreted {
					t.Fatalf("compiled=%+v interpreted=%+v", compiled, interpreted)
				}
				return
			}
			if compiled.status != 2 {
				t.Fatalf("status=%d stderr=%q", compiled.status, compiled.stderr)
			}
			if !strings.HasPrefix(compiled.stderr, code) || !strings.HasPrefix(interpreted.stderr, code) {
				t.Fatalf("compiled=%q interpreted=%q", compiled.stderr, interpreted.stderr)
			}
		})
	}
}

// TestReadonlyEmitterCapturesOperandsOnce pins the emitted shape: index
// operands are captured once, in source order, before the check, and both the
// check and the update then use those temps.
func TestReadonlyEmitterCapturesOperandsOnce(t *testing.T) {
	const src = `cfg := map[string][]int{"ports": {80, 443}}
readonly cfg
cfg["ports"][0] = 8080
`
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "readonly.bpp")
	if err != nil {
		t.Fatal(err)
	}
	e := newReadonlyEmitter()
	harness := &readonlyEmit{e: e, kinds: []string{"slice"}, t: t}
	body, err := harness.stmts(file.Stmts)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`__test_state.Mark("cfg", &cfg)`,
		"__test_readonlyIndex0 := \"ports\"\n__test_readonlyIndex1 := 0\n__test_readonlyContainer := cfg[__test_readonlyIndex0]\n",
		`__test_state.CheckMutation("cfg", &cfg, __test_readonlyContainer, "[\"ports\"][0]", "slice")`,
		"__test_readonlyContainer[__test_readonlyIndex1] = 8080",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Count(body, `"ports"`) != 2 { // one capture, one diagnostic path label
		t.Fatalf("index evaluated more than once:\n%s", body)
	}
}

func TestReadonlyEmitterRequiresExplicitStateAndBoundary(t *testing.T) {
	e := newReadonlyEmitter()
	e.bind("cfg")
	for _, c := range []ReadonlyContext{{}, {State: "state"}, {Abort: "abort"}} {
		if _, err := e.readonlyRebind(nil, c, "cfg", "1"); err == nil {
			t.Fatalf("accepted %+v", c)
		}
		if _, _, err := e.readonlyBuiltin(nil, c, ReadonlyBuiltin{Name: "clear", Root: "cfg", Target: "cfg"}); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}

func TestReadonlyEmitterRejectsIllFormedSites(t *testing.T) {
	e := newReadonlyEmitter()
	e.bind("cfg")
	ident := &syntax.BashPPIdent{Name: &syntax.Lit{Value: "cfg"}}
	field := &syntax.BashPPSelectorExpr{X: ident, Sel: &syntax.Lit{Value: "Name"}}
	index := &syntax.BashPPIndexExpr{X: ident, Index: &syntax.BashPPBasicLit{Value: &syntax.Lit{Value: "0"}}}
	cases := []struct {
		name   string
		target syntax.BashPPExpr
		m      ReadonlyMutation
	}{
		{"unknown kind", field, ReadonlyMutation{Kind: "object", Value: "1"}},
		{"missing value", field, ReadonlyMutation{Kind: "field"}},
		{"whole binding", ident, ReadonlyMutation{Kind: "field", Value: "1"}},
		{"kind disagrees with path", index, ReadonlyMutation{Kind: "field", Value: "1"}},
		{"field kind for index path", field, ReadonlyMutation{Kind: "slice", Value: "1"}},
	}
	for _, tc := range cases {
		if _, err := e.readonlyMutation(tc.target, readonlyContextNames, tc.m); err == nil {
			t.Fatalf("%s accepted", tc.name)
		}
	}
	unknown := &syntax.BashPPSelectorExpr{X: &syntax.BashPPIdent{Name: &syntax.Lit{Value: "missing"}}, Sel: &syntax.Lit{Value: "Name"}}
	if _, err := e.readonlyMutation(unknown, readonlyContextNames, ReadonlyMutation{Kind: "field", Value: "1"}); err == nil {
		t.Fatal("undefined root accepted")
	}
	if _, _, err := e.readonlyBuiltin(nil, readonlyContextNames, ReadonlyBuiltin{Name: "delete", Root: "cfg", Target: "cfg"}); err == nil {
		t.Fatal("delete without a key accepted")
	}
	if _, _, err := e.readonlyBuiltin(nil, readonlyContextNames, ReadonlyBuiltin{Name: "sort", Root: "cfg", Target: "cfg"}); err == nil {
		t.Fatal("unguarded builtin accepted")
	}
	declare := &syntax.DeclClause{Variant: &syntax.Lit{Value: "export"}, Args: []*syntax.Assign{{Name: &syntax.Lit{Value: "cfg"}}}}
	if _, err := e.readonlyDeclare(declare, readonlyContextNames); err == nil {
		t.Fatal("export marked as readonly")
	}
}

// TestReadonlyEmitterTypedValueCapture pins the opt-in capture: with the
// target's element type the value is captured before the check, which is the
// only form that can keep an untyped constant's assignability.
func TestReadonlyEmitterTypedValueCapture(t *testing.T) {
	e := newReadonlyEmitter()
	e.bind("cfg")
	target := &syntax.BashPPIndexExpr{
		X:     &syntax.BashPPIdent{Name: &syntax.Lit{Value: "cfg"}},
		Index: &syntax.BashPPBasicLit{Value: &syntax.Lit{Value: `"k"`}},
	}
	text, err := e.readonlyMutation(target, readonlyContextNames, ReadonlyMutation{Kind: "map", Value: "1", ValueType: "float64"})
	if err != nil {
		t.Fatal(err)
	}
	capture := strings.Index(text, "var __test_readonlyValue float64 = 1")
	check := strings.Index(text, "CheckMutation")
	if capture < 0 || check < 0 || capture > check {
		t.Fatalf("value capture must precede the check:\n%s", text)
	}
	if !strings.HasSuffix(strings.TrimSpace(text), "__test_readonlyContainer[__test_readonlyIndex0] = __test_readonlyValue\n}") {
		t.Fatalf("update must use the captured value:\n%s", text)
	}
}

// TestReadonlyEmitterSpreadAppendGuard pins the runtime-length form: the spread
// operand is captured once and decides both the guard and the call.
func TestReadonlyEmitterSpreadAppendGuard(t *testing.T) {
	e := newReadonlyEmitter()
	e.bind("s")
	e.bind("more")
	setup, call, err := e.readonlyBuiltin(nil, readonlyContextNames, ReadonlyBuiltin{
		Name: "append", Root: "s", Target: "s", Path: "s", Args: []string{"more"}, Spread: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"__test_readonlyTarget := s\n__test_readonlySpread := more\n",
		"if len(__test_readonlySpread) > 0 && len(__test_readonlyTarget)+len(__test_readonlySpread) <= cap(__test_readonlyTarget) {",
		`__test_state.CheckMutation("s", &s, __test_readonlyTarget, "s", "append")`,
	} {
		if !strings.Contains(setup, want) {
			t.Fatalf("missing %q in:\n%s", want, setup)
		}
	}
	if call != "append(__test_readonlyTarget, __test_readonlySpread...)" {
		t.Fatalf("call=%q", call)
	}
	if _, _, err := e.readonlyBuiltin(nil, readonlyContextNames, ReadonlyBuiltin{
		Name: "copy", Root: "s", Target: "s", Args: []string{"more"}, Spread: true,
	}); err == nil {
		t.Fatal("spread copy accepted")
	}
}
