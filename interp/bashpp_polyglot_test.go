package interp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestBashPPPythonUsesSourceEnvironmentPlan(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	runtime := filepath.Join(root, "planned-python")
	body := "#!/bin/sh\nexport BASHPP_SELECTED_RUNTIME=yes\nexec " + strconv.Quote(python) + " \"$@\"\n"
	if err := os.WriteFile(runtime, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bashpp.yaml"), []byte("runtime: planned-python\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "program.bpp")
	source := "~~~python\ndef planned() -> str:\n    import os\n    return os.environ.get('BASHPP_SELECTED_RUNTIME', '') + ':' + os.getcwd()\n~~~\nvalue := planned()\necho \"$value\"\n"
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(source), name)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(t.TempDir()), interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), file); err != nil || stderr.Len() != 0 || stdout.String() != "yes:"+canonicalRoot+"\n" {
		t.Fatalf("stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), err)
	}
}

func TestBashPPNonPythonDoesNotDiscoverEnvironment(t *testing.T) {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader("echo ok\n"), filepath.Join(t.TempDir(), "missing", "script.bpp"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	env := expand.ListEnviron("BASHPP_PYTHON=/definitely/missing", "PATH="+os.Getenv("PATH"))
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Env(env), interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(t.Context(), file); err != nil || stderr.Len() != 0 || stdout.String() != "ok\n" {
		t.Fatalf("stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), err)
	}
}

func runPolyglot(t *testing.T, source string) (string, string, error) {
	t.Helper()
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(source), "polyglot.bpp")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(t.Context(), file)
	return stdout.String(), stderr.String(), err
}

func TestBashPPRustDirectAndQualifiedCalls(t *testing.T) {
	if _, err := exec.LookPath("rustc"); err != nil {
		t.Skip("rustc unavailable")
	}
	direct := `~~~rust
pub fn add(a: i64, b: i64) -> i64 { println!("rust"); a + b }
~~~
x := add(20, 22)
echo "x=$x"
`
	out, diagnostic, err := runPolyglot(t, direct)
	if err != nil || out != "rust\nx=42\n" || diagnostic != "" {
		t.Fatalf("direct: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
	qualified := `~~~rs as native
pub fn greet(name: &str) -> String { format!("hello {name}") }
~~~
value := native.greet(world)
echo "$value"
`
	out, diagnostic, err = runPolyglot(t, qualified)
	if err != nil || out != "hello world\n" || diagnostic != "" {
		t.Fatalf("qualified: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
}

func TestBashPPGoDirectAndQualifiedCalls(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	direct := `~~~go
import "fmt"
func Add(a int64, b int64) int64 { fmt.Println("go"); return a+b }
~~~
x := Add(20, 22)
echo "x=$x"
`
	out, diagnostic, err := runPolyglot(t, direct)
	if err != nil || out != "go\nx=42\n" || diagnostic != "" {
		t.Fatalf("direct: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
	qualified := `~~~go as go
func Greet(name string) string { return "hello "+name }
~~~
value := go.Greet(world)
echo "$value"
`
	out, diagnostic, err = runPolyglot(t, qualified)
	if err != nil || out != "hello world\n" || diagnostic != "" {
		t.Fatalf("qualified: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
}

func TestBashPPShellDialectIslands(t *testing.T) {
	bashSource := `~~~bash as island
var() { printf '%s:%s:%s' "$1" "$2" "$3"; }
Show() { counter=$(( ${counter:-0} + 1 )); var "$1" = "$counter"; }
~~~
first := island.Show(alpha)
second := island.Show(alpha)
printf '%s|%s\n' "$first" "$second"
`
	out, diagnostic, err := runPolyglot(t, bashSource)
	if err != nil || out != "alpha:=:1|alpha:=:1\n" || diagnostic != "" {
		t.Fatalf("bash island: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}

	shSource := `~~~sh as posix
Join() { printf '%s/%s/%s' "$#" "$1" "$2"; }
~~~
value := posix.Join("a b", c)
echo "$value"
`
	out, diagnostic, err = runPolyglot(t, shSource)
	if err != nil || out != "2/a b/c\n" || diagnostic != "" {
		t.Fatalf("POSIX island: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
}

func TestBashPPShellDialectIslandStatusIsCallError(t *testing.T) {
	source := `~~~sh as posix
Fail() { echo detail >&2; return 7; }
~~~
value := posix.Fail()
`
	out, diagnostic, err := runPolyglot(t, source)
	if err == nil || out != "" || !strings.Contains(diagnostic, "detail") || !strings.Contains(diagnostic, "status 7") {
		t.Fatalf("out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
}

func TestBashPPCAndCPPDirectAndQualifiedCalls(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang unavailable")
	}
	direct := `~~~c
#include <stdint.h>
int64_t add(int64_t a, int64_t b) { return a + b; }
~~~
x := add(20, 22)
echo "c=$x"
`
	out, diagnostic, err := runPolyglot(t, direct)
	if err != nil || out != "c=42\n" || diagnostic != "" {
		t.Fatalf("C: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
	qualified := `~~~cxx as native
#include <string>
std::string greet(const std::string& name) { return "hello "+name; }
~~~
value := native.greet(world)
echo "$value"
`
	out, diagnostic, err = runPolyglot(t, qualified)
	if err != nil || out != "hello world\n" || diagnostic != "" {
		t.Fatalf("C++: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
}

func TestBashPPPythonSourcedFileIsolation(t *testing.T) {
	child := filepath.Join(t.TempDir(), "child.bpp")
	err := os.WriteFile(child, []byte("~~~python\ndef answer() -> int:\n    return 2\n~~~\nchildX := answer()\necho child=$childX\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	outer := "~~~python\ndef answer() -> int:\n    return 1\n~~~\n. " + strconv.Quote(child) + "\nx := answer()\necho outer=$x\n"
	out, diagnostic, err := runPolyglot(t, outer)
	if err != nil || out != "child=2\nouter=1\n" || diagnostic != "" {
		t.Fatalf("out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
}

func TestBashPPPythonDirectAndQualifiedCalls(t *testing.T) {
	direct := `~~~python
def add(a: int, b: int) -> int:
    return a+b
def announce(value: str) -> None:
    print(value)
def answer() -> int:
    return 42
~~~
x := add(2, 3)
echo "x=$x"
announce(done)
y := answer()
echo "y=$y"
`
	out, diagnostic, err := runPolyglot(t, direct)
	if err != nil || out != "x=5\ndone\ny=42\n" || diagnostic != "" {
		t.Fatalf("direct: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}

	qualified := `~~~python as py
def add(a: int, b: int) -> int:
    return a+b
~~~
x := py.add(4, 5)
echo "x=$x"
`
	out, diagnostic, err = runPolyglot(t, qualified)
	if err != nil || out != "x=9\n" || diagnostic != "" {
		t.Fatalf("qualified: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
}

// `~~~py` is an alias spelling of `~~~python`: both the aliased and the naked
// forms run through the same Python module plan, and a `py` alias on a `py`
// fence is the documented `py.main()` shape.
func TestBashPPPythonPyAliasSpelling(t *testing.T) {
	qualified := `~~~py as py
def main() -> str:
    return "launched"
~~~
value := py.main()
echo "value=$value"
`
	out, diagnostic, err := runPolyglot(t, qualified)
	if err != nil || out != "value=launched\n" || diagnostic != "" {
		t.Fatalf("qualified: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}

	naked := `~~~py
def answer() -> int:
    return 42
~~~
x := answer()
echo "x=$x"
`
	out, diagnostic, err = runPolyglot(t, naked)
	if err != nil || out != "x=42\n" || diagnostic != "" {
		t.Fatalf("naked: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}

	// One module per language per source unit: a `py` block and a `python`
	// block are the SAME language, so they must agree on the alias.
	mixed := `~~~python as py
def one() -> int:
    return 1
~~~
~~~py
def two() -> int:
    return 2
~~~
echo unreachable
`
	if _, _, err := runPolyglot(t, mixed); err == nil || !strings.Contains(err.Error(), "inconsistent aliases") {
		t.Fatalf("mixed spellings with different aliases: err=%v", err)
	}
}

func TestBashPPPythonDynamicAndCollisions(t *testing.T) {
	dynamic := `~~~python
def loose(value):
    return value+"!"
def fail():
    raise ValueError("boom")
~~~
value, callErr := loose(ok)
echo "$value:${callErr:+error}"
failed, failErr := fail()
echo "${failErr:+caught}"
`
	out, diagnostic, err := runPolyglot(t, dynamic)
	if err != nil || out != "ok!:\ncaught\n" || diagnostic != "" {
		t.Fatalf("dynamic: out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}

	collisions := []string{`~~~python
def same(a: int) -> int:
    return a
~~~
func same(a int) int { return a }
	`, `same() { :; }
~~~python
def same(a: int) -> int:
    return a
~~~
`, `~~~python
def same(a: int) -> int:
    return a
~~~
var same = 1
`}
	for _, collision := range collisions {
		_, _, err = runPolyglot(t, collision)
		if err == nil || !strings.Contains(err.Error(), "collides") {
			t.Fatalf("collision error = %v for %q", err, collision)
		}
	}
}

func TestBashPPPythonModuleIsNotShellState(t *testing.T) {
	source := `~~~python as py
def value() -> int:
    return 1
~~~
env | grep '^py=' || true
`
	out, _, err := runPolyglot(t, source)
	if err != nil || out != "" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPTypeScriptDirectQualifiedAndCoexistence(t *testing.T) {
	if os.Getenv("BASHPP_TYPESCRIPT_MODULE") == "" {
		t.Skip("set BASHPP_TYPESCRIPT_MODULE to an official TypeScript compiler module")
	}
	source := `~~~python as py
def twice(value: int) -> int:
    return value * 2
~~~
~~~typescript
interface Label { value: string }
export function add(a: number, b: number): number { console.log("ts"); return a + b }
function answer(): number { return 42 }
~~~
x := add(20, 22)
y := answer()
z := py.twice(3)
echo "$x:$y:$z"
`
	out, diagnostic, err := runPolyglot(t, source)
	if err != nil || out != "ts\n42:42:6\n" || diagnostic != "" {
		t.Fatalf("out=%q diagnostic=%q err=%v", out, diagnostic, err)
	}
}
