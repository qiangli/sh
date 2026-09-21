//go:build full

package lower_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower"
	"mvdan.cc/sh/v3/syntax"
)

func TestPythonFenceInterpretedNativeParity(t *testing.T) {
	tests := map[string]string{
		"typed direct": `~~~python
def add(a: int, b: int) -> int:
    print("python")
    return a+b
~~~
x := add(2, 3)
echo "x=$x"
`,
		"typed qualified": `~~~python as py
def add(a: int, b: int) -> int:
    return a+b
~~~
x := py.add(4, 5)
echo "x=$x"
`,
		"dynamic result": `~~~python
def loose(value):
    return value+"!"
~~~
x, callErr := loose(ok)
echo "$x:$callErr"
`,
		"dynamic structured error": `~~~python
def fail():
    raise ValueError("boom")
~~~
value, callErr := fail()
echo "$value:$callErr"
`,
		"typed structured error opt in": `~~~python
def fail() -> int:
    raise ValueError("boom")
~~~
value, callErr := fail()
echo "value=$value err=$callErr status=$?"
`,
		"typed structured success opt in": `~~~python
def ok() -> int:
    return 7
~~~
value, callErr := ok()
echo "value=$value err=$callErr status=$?"
`,
		"py alias launcher": `~~~py as py
def main() -> str:
    return "launched"
~~~
value := py.main()
echo "value=$value"
`,
		"structured object round trip": `~~~python
def record() -> dict[str, list[int]]:
    return {"items": [3, 4]}
def bounce(value: dict[str, list[int]]) -> dict[str, list[int]]:
    return value
~~~
value := record()
again := bounce(value)
echo "$value|$again"
`,
		"structured object qualified": `~~~python as py
def record() -> dict[str, list[int]]:
    return {"items": [3, 4]}
~~~
value := py.record()
echo "$value"
`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) { testPythonFenceInterpretedNativeParity(t, source) })
	}
}

func TestTypeScriptFenceInterpretedNativeParity(t *testing.T) {
	if os.Getenv("BASHPP_TYPESCRIPT_MODULE") == "" {
		t.Skip("set BASHPP_TYPESCRIPT_MODULE to an official TypeScript compiler module")
	}
	testPythonFenceInterpretedNativeParity(t, `~~~typescript as ts
type Numeric = number
export function add(a: Numeric, b: Numeric): number { console.log("typescript"); return a + b }
~~~
x := ts.add(20, 22)
echo "x=$x"
`)
}

func requireRustToolchain(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"rustc", "cargo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
}

func TestRustFenceInterpretedNativeParity(t *testing.T) {
	requireRustToolchain(t)
	got := testPythonFenceInterpretedNativeParityAt(t, `~~~rust as rs
pub fn add(a: i64, b: i64) -> i64 { println!("rust"); a + b }
pub fn checked(value: i64) -> Result<i64, String> { if value < 0 { Err("negative".into()) } else { Ok(value) } }
~~~
x := rs.add(20, 22)
echo "x=$x"
`, "input.bpp")
	if got != "rust\nx=42\n" {
		t.Fatalf("output = %q", got)
	}
	got = testPythonFenceInterpretedNativeParityAt(t, `~~~rust as rs
pub fn checked() -> Result<i64, String> { Err("negative".into()) }
~~~
value, callErr := rs.checked()
echo "value=$value err=$callErr status=$?"
`, "input.bpp")
	if got != "value=0 err=RUST-ECALL: negative status=0\n" {
		t.Fatalf("output = %q", got)
	}
}

// Ordinary serde structs and Vec<Struct> cross the Object mapping identically
// in the interpreter and the lowered program, island stdout/stderr included,
// and the one persistent worker keeps its state in both. Fixture shapes follow
// serde_json v1.0.151 tests/test.rs `test_parse_struct` (MIT OR Apache-2.0).
func TestRustFenceSerdeStructParity(t *testing.T) {
	requireRustToolchain(t)
	got := testPythonFenceInterpretedNativeParityAt(t, `~~~rust as rs
use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize)]
pub struct Inner { pub b: usize, pub c: Vec<String> }

#[derive(Serialize, Deserialize)]
pub struct Outer { pub inner: Vec<Inner> }

pub fn make(b: usize) -> Outer { println!("made"); Outer { inner: vec![Inner { b, c: vec!["abc".into(), "xyz".into()] }] } }
pub fn total(outer: Outer) -> usize { eprintln!("totalling"); outer.inner.iter().map(|inner| inner.b).sum() }
pub fn bounce(outers: Vec<Outer>) -> Vec<Outer> { outers }
pub fn empty() -> Vec<Outer> { Vec::new() }
pub fn bump() -> i64 {
    static COUNT: std::sync::atomic::AtomicI64 = std::sync::atomic::AtomicI64::new(0);
    COUNT.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1
}
~~~
value := rs.make(2)
sum := rs.total(value)
none := rs.empty()
again := rs.bounce(none)
a := rs.bump()
b := rs.bump()
echo "$value|$sum|$none|$again|$a$b"
`, "input.bpp")
	if got != "made\n{\"inner\":[{\"b\":2,\"c\":[\"abc\",\"xyz\"]}]}|2|[]|[]|12\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestGoFenceInterpretedNativeParity(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	got := testPythonFenceInterpretedNativeParityAt(t, `~~~go as go
import "fmt"
func Add(a int64, b int64) int64 { fmt.Println("go"); return a+b }
func Checked(value int64) (int64, error) { if value < 0 { return 0, fmt.Errorf("negative") }; return value, nil }
~~~
x := go.Add(20, 22)
echo "x=$x"
`, filepath.Join("testdata", "input.bpp"))
	if got != "go\nx=42\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestShellFenceInterpretedNativeParity(t *testing.T) {
	for name, source := range map[string]string{
		"bash": `~~~bash as island
Show() { local value=$1; printf 'bash:%s:%s' "$#" "$value"; }
~~~
value := island.Show("a b", c)
echo "$value"
`,
		"posix": `~~~sh as island
Show() { value=$1; printf 'posix:%s:%s' "$#" "$value"; }
~~~
value := island.Show("a b", c)
echo "$value"
`,
	} {
		t.Run(name, func(t *testing.T) {
			got := testPythonFenceInterpretedNativeParityAt(t, source, filepath.Join("testdata", "input.bpp"))
			if want := name + ":2:a b\n"; got != want {
				t.Fatalf("output = %q, want %q", got, want)
			}
		})
	}
}

func TestCAndCPPFenceInterpretedNativeParity(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang unavailable")
	}
	for name, source := range map[string]string{
		"c": `~~~c as native
#include <stdint.h>
int64_t add(int64_t a, int64_t b) { return a+b; }
~~~
x := native.add(20, 22)
echo "c=$x"
`,
		"cpp": `~~~cpp as native
#include <string>
std::string greet(const std::string& name) { return "hello "+name; }
~~~
value := native.greet(world)
echo "$value"
`,
	} {
		t.Run(name, func(t *testing.T) { testPythonFenceInterpretedNativeParity(t, source) })
	}
}

func TestPythonFenceUsesSourceEnvironmentPlan(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	launcher := filepath.Join(root, "planned-python")
	body := "#!/bin/sh\nexport BASHPP_SELECTED_RUNTIME=yes\nexec " + strconv.Quote(python) + " \"$@\"\n"
	if err := os.WriteFile(launcher, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bashpp.yaml"), []byte("runtime: planned-python\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	source := "~~~python\ndef planned() -> str:\n    import os\n    return os.environ.get('BASHPP_SELECTED_RUNTIME', '') + ':' + os.getcwd()\n~~~\nvalue := planned()\necho \"$value\"\n"
	got := testPythonFenceInterpretedNativeParityAt(t, source, filepath.Join(root, "program.bpp"))
	if got != "yes:"+canonicalRoot+"\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestLowerNonPythonDoesNotDiscoverEnvironment(t *testing.T) {
	t.Setenv("BASHPP_PYTHON", filepath.Join(t.TempDir(), "missing-python"))
	file := parse(t, "echo ok\n", filepath.Join(t.TempDir(), "missing", "input.bpp"))
	if _, err := lower.Compile(file, lower.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestPythonFenceEnvironmentUsesOrigin(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	launcher := filepath.Join(root, "origin-python")
	body := "#!/bin/sh\nexec " + strconv.Quote(python) + " \"$@\"\n"
	if err := os.WriteFile(launcher, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bashpp.yaml"), []byte("runtime: origin-python\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	origin := filepath.Join(root, "program.bpp")
	if err := os.WriteFile(origin, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	file := parse(t, "~~~python\ndef answer() -> int:\n    return 42\n~~~\n", "<stdin>")
	result, err := lower.Compile(file, lower.Options{Origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	canonicalLauncher, err := filepath.EvalSymlinks(launcher)
	if err != nil {
		t.Fatal(err)
	}
	generated := string(result.Source)
	if !strings.Contains(generated, "Dir: "+strconv.Quote(canonicalRoot)) || !strings.Contains(generated, "Executable: "+strconv.Quote(canonicalLauncher)) {
		t.Fatalf("generated plan did not use Origin project:\n%s", generated)
	}
}

func testPythonFenceInterpretedNativeParity(t *testing.T, source string) {
	testPythonFenceInterpretedNativeParityAt(t, source, "input.bpp")
}

func testPythonFenceInterpretedNativeParityAt(t *testing.T, source, filename string) string {
	t.Helper()
	file := parse(t, source, filename)
	result, err := lower.Compile(file, lower.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Source), "polyglot.Start") {
		t.Fatalf("generated source omitted module:\n%s", result.Source)
	}

	var interpreted, interpretedErr bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &interpreted, &interpretedErr))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if err := runner.Run(ctx, file); err != nil {
		t.Fatalf("interpreted: %v: %s", err, interpretedErr.String())
	}

	dir := t.TempDir()
	generated := filepath.Join(dir, "generated.go")
	if err := os.WriteFile(generated, result.Source, 0600); err != nil {
		t.Fatal(err)
	}
	_, this, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(this))
	module := "module polyglotfixture\n\ngo " + strings.TrimPrefix(runtime.Version(), "go") + "\nrequire mvdan.cc/sh/v3 v3.0.0\nreplace mvdan.cc/sh/v3 => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "program")
	cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-mod=mod", "-o", binary, "generated.go")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s\n%s", err, output, result.Source)
	}
	cmd = exec.CommandContext(ctx, binary)
	cmd.Dir = t.TempDir()
	var native, nativeErr bytes.Buffer
	cmd.Stdout = &native
	cmd.Stderr = &nativeErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("native: %v: %s", err, nativeErr.String())
	}
	if native.String() != interpreted.String() || nativeErr.String() != interpretedErr.String() {
		t.Fatalf("interpreted=(%q,%q) native=(%q,%q)", interpreted.String(), interpretedErr.String(), native.String(), nativeErr.String())
	}
	return native.String()
}
