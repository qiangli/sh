package lower_test

import (
	"bytes"
	"context"
	"errors"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower"
	"mvdan.cc/sh/v3/syntax"
)

func parse(t *testing.T, source, name string) *syntax.File {
	t.Helper()
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(source), name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

type compiledCase struct {
	*lower.Result
	input string
}

func compile(t *testing.T, source string) compiledCase {
	t.Helper()
	r, err := lower.Compile(parse(t, source, "input.bpp"), lower.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return compiledCase{r, source}
}
func execute(t *testing.T, r compiledCase) (string, string) { return executeBuild(t, r) }
func executeBuild(t *testing.T, r compiledCase, flags ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "generated.go")
	if err := os.WriteFile(p, r.Source, 0600); err != nil {
		t.Fatal(err)
	}
	_, this, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(this))
	module := "module lowerfixture\n\ngo " + strings.TrimPrefix(runtime.Version(), "go") + "\n"
	if strings.Contains(string(r.Source), "lower/shellrt") {
		module += "require mvdan.cc/sh/v3 v3.0.0\nreplace mvdan.cc/sh/v3 => " + root + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	binary := filepath.Join(dir, "program")
	args := append([]string{"build", "-mod=mod"}, flags...)
	args = append(args, "-o", binary, "generated.go")
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s\n%s", err, out, r.Source)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(ctx, binary)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=/no-tools"}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	compiledStatus := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		compiledStatus = exit.ExitCode()
	}
	var interpOut, interpErr bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &interpOut, &interpErr), interp.Dir(cmd.Dir), interp.Env(expand.ListEnviron("PATH=/no-tools")))
	if err != nil {
		t.Fatal(err)
	}
	interpretedStatus := 0
	if err := runner.Run(ctx, parse(t, r.input, "input.bpp")); err != nil {
		var exit interp.ExitStatus
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		interpretedStatus = int(exit)
	}
	if compiledStatus != interpretedStatus {
		t.Fatalf("status compiled=%d interpreted=%d; compiled=(%q,%q), interpreted=(%q,%q)", compiledStatus, interpretedStatus, out.String(), stderr.String(), interpOut.String(), interpErr.String())
	}
	if out.String() != interpOut.String() || stderr.String() != interpErr.String() {
		t.Fatalf("parity mismatch: compiled=(%q,%q), interpreted=(%q,%q)\n%s", out.String(), stderr.String(), interpOut.String(), interpErr.String(), r.Source)
	}
	return out.String(), stderr.String()
}

func TestNativeVerticalSlice(t *testing.T) {
	r := compile(t, `func sum(n int) int {
 total := 0
 for i := 0; i < n; i++ {
  if i == 2 { continue }
  total += i
 }
 return total
}
answer := sum(5)
println(answer)
`)
	for _, forbidden := range []string{"interp", "syntax", "shellrt", "exec.Command"} {
		if strings.Contains(string(r.Source), forbidden) {
			t.Fatalf("native program depends on %s", forbidden)
		}
	}
	if len(r.Imports) != 1 || r.Imports[0] != "fmt" {
		t.Fatal(r.Imports)
	}
	out, stderr := execute(t, r)
	if out != "8\n" || stderr != "" {
		t.Fatalf("out=%q stderr=%q", out, stderr)
	}
}
func TestMixedOrderedState(t *testing.T) {
	r := compile(t, `func twice(n int) int {
 return $((n * 2))
}
printf 'before\n'
var x int = 4
printf '%d\n' "$x"
x=7
y := twice(x)
printf 'after:%d\n' "$y"
`)
	out, stderr := execute(t, r)
	if out != "before\n4\nafter:14\n" || stderr != "" {
		t.Fatalf("out=%q stderr=%q", out, stderr)
	}
}
func TestTupleAndNamedResult(t *testing.T) {
	r := compile(t, `func pair(a int) (int, int) { return a, 2 }
func named() (n int) { n=5; return }
a, b := pair(3)
c := named()
printf '%d:%d:%d\n' "$a" "$b" "$c"
`)
	out, stderr := execute(t, r)
	if out != "3:2:5\n" || stderr != "" {
		t.Fatalf("%q %q", out, stderr)
	}
}
func TestInitializerSideEffectsStayInOrder(t *testing.T) {
	r := compile(t, `func produce() int { printf 'middle\n'; return 3 }
printf 'first\n'
x := produce()
printf 'last:%d\n' "$x"
`)
	out, _ := execute(t, r)
	if out != "first\nmiddle\nlast:3\n" {
		t.Fatal(out)
	}
}
func TestDeterminismMappingsAndEmptyProgram(t *testing.T) {
	source := `func add(a int, b int) int {
 total := a + b
 return total
}
n := add(2, 3)
println(n)
`
	a, err := lower.Compile(parse(t, source, "a.bpp"), lower.Options{Origin: "/one/input.bpp"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := lower.Compile(parse(t, source, "b.bpp"), lower.Options{Origin: "/two/input.bpp"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Source, b.Source) {
		t.Fatal("filename affects output")
	}
	canonical, err := format.Source(a.Source)
	if err != nil || !bytes.Equal(canonical, a.Source) {
		t.Fatal("noncanonical output")
	}
	if len(a.Mappings) < 4 {
		t.Fatalf("mapping count %d", len(a.Mappings))
	}
	for _, m := range a.Mappings {
		got, ok := a.LookupLine(m.GoLine)
		if !ok || got != m || !m.Pos.IsValid() {
			t.Fatal("invalid mapping", m)
		}
	}
	if _, ok := a.LookupLine(-1); ok {
		t.Fatal("invalid line matched")
	}
	out, stderr := execute(t, compile(t, ""))
	if out != "" || stderr != "" {
		t.Fatal(out, stderr)
	}
}
func TestRejectsUnsupportedAndInvalidWithoutResult(t *testing.T) {
	cases := []struct{ name, source, code string }{
		{"unknown", `func f() int { return missing }`, lower.CodeUndefined},
		{"badtype", `func f() int { return "wrong" }`, lower.CodeType},
		{"arity", `func f(n int) int { return n }; x := f()`, lower.CodeType},
		{"redirect", `printf hi >out`, lower.CodeUnsupported},

		{"scope", `func f() int { if true { x := 1 }; return x }`, lower.CodeUndefined},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := lower.Compile(parse(t, tc.source, "bad.bpp"), lower.Options{})
			var list lower.ErrorList
			if r != nil || !errors.As(err, &list) || len(list) == 0 || list[0].Code != tc.code || !list[0].Pos.IsValid() {
				t.Fatalf("result=%v error=%v", r, err)
			}
		})
	}
}

func TestShellWordScopeAndFailureRestoration(t *testing.T) {
	r := compile(t, `var x int = 4
printf '%s\n' x
printf '%d\n' x
func show(n int) { printf '%d\n' n }
show(x)
printf 'recovered\n'
`)
	out, stderr := execute(t, r)
	if out != "x\n0\n4\nrecovered\n" || stderr != "printf: x: invalid number\n" {
		t.Fatalf("out=%q stderr=%q", out, stderr)
	}
}
func TestNativePrintSpacing(t *testing.T) {
	r := compile(t, `print(1, 2)
println("a", 3)
`)
	out, stderr := execute(t, r)
	if out != "12a 3\n" || stderr != "" {
		t.Fatalf("%q %q", out, stderr)
	}
}
func TestUnusedLocalsAndPrivateNameCollision(t *testing.T) {
	r := compile(t, `func answer(__bpp0_arg int) int {
 unused := 2
 return __bpp0_arg
}
a := answer(7)
printf '%d\n' "$a"
`)
	out, _ := execute(t, r)
	if out != "7\n" {
		t.Fatal(out)
	}
}
func TestFormatUnsupportedRejected(t *testing.T) {
	for _, src := range []string{`printf '%q' value`, `printf '%04d' 3`, `printf '\x41'`} {
		r, err := lower.Compile(parse(t, src, "format.bpp"), lower.Options{})
		if r != nil || err == nil {
			t.Fatalf("accepted unsupported format: %s", src)
		}
	}
}

func TestFinalPrintfFailureStatus(t *testing.T) {
	r := compile(t, `printf '%d\n' bad`)
	out, stderr := execute(t, r)
	if out != "0\n" || stderr != "printf: bad: invalid number\n" {
		t.Fatalf("%q %q", out, stderr)
	}
}
