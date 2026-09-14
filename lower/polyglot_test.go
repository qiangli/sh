package lower_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) { testPythonFenceInterpretedNativeParity(t, source) })
	}
}

func testPythonFenceInterpretedNativeParity(t *testing.T, source string) {
	t.Helper()
	file := parse(t, source, "input.bpp")
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
}
