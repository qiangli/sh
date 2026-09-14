package polyglot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPythonUnavailable(t *testing.T) {
	python := Python{Command: t.TempDir() + "/missing-python"}
	_, err := python.Analyze(context.Background(), "def f(): pass\n")
	if err == nil || !strings.Contains(err.Error(), "Python runtime unavailable") {
		t.Fatalf("analyze error = %v", err)
	}
	m := Start(Plan{Language: "python", Source: "def f(): pass\n"}, python)
	_, err = m.Call(context.Background(), "f")
	if err == nil || !strings.Contains(err.Error(), "Python runtime unavailable") {
		t.Fatalf("call error = %v", err)
	}
	if closeErr := m.Close(); closeErr != nil && !errors.Is(closeErr, context.Canceled) {
		t.Fatalf("close = %v", closeErr)
	}
}

func pythonPlan(t *testing.T, source string) Plan {
	t.Helper()
	plans, err := Prepare(context.Background(), []Block{{Language: "python", Source: source}}, map[string]Analyzer{"python": Python{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %d", len(plans))
	}
	return plans[0]
}

func TestPythonAnalyzeDeclarationOnlyAndSignatures(t *testing.T) {
	plan := pythonPlan(t, `"""module docs"""
def add(a: int, b: int) -> int:
    return a+b
def loose(value):
    return value
def _private():
    return 1
`)
	if len(plan.Exports) != 2 {
		t.Fatalf("exports = %#v", plan.Exports)
	}
	if got := plan.Exports[0]; got.Name != "add" || got.Signature.Dynamic || strings.Join(got.Signature.Params, ",") != "int,int" || strings.Join(got.Signature.Results, ",") != "int" {
		t.Fatalf("typed export = %#v", got)
	}
	if !plan.Exports[1].Signature.Dynamic {
		t.Fatalf("dynamic export = %#v", plan.Exports[1])
	}
	for _, source := range []string{"x = 1\n", "import os\n", "class X: pass\n", "async def f(): pass\n", "@staticmethod\ndef f(): pass\n"} {
		if _, err := Prepare(context.Background(), []Block{{Language: "python", Source: source}}, map[string]Analyzer{"python": Python{}}); err == nil {
			t.Fatalf("accepted %q", source)
		}
	}
}

func TestPythonPersistentCallsValuesOutputAndRestart(t *testing.T) {
	plan := pythonPlan(t, `
def add(a: int, b: int) -> int:
    print("called")
    return a+b
def blob(value: bytes) -> bytes:
    return value+b"!"
def fail():
    raise ValueError("boom")
def lies() -> int:
    return "not an int"
def hang():
    while True: pass
`)
	m := Start(plan, Python{})
	defer m.Close()
	got, err := m.Call(context.Background(), "add", int64(2), int64(3))
	if err != nil || got.Value != int64(5) || got.Stdout != "called\n" {
		t.Fatalf("call = %#v, %v", got, err)
	}
	blob, err := m.Call(context.Background(), "blob", []byte("x"))
	if err != nil || string(blob.Value.([]byte)) != "x!" {
		t.Fatalf("blob = %#v, %v", blob, err)
	}
	if _, err := m.Call(context.Background(), "fail"); err == nil || !strings.Contains(err.Error(), "ValueError: boom") {
		t.Fatalf("error = %v", err)
	}
	if _, err := m.Call(context.Background(), "lies"); err == nil || !strings.Contains(err.Error(), "violated its int result annotation") {
		t.Fatalf("annotation error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := m.Call(ctx, "hang"); err == nil {
		t.Fatal("hang was not canceled")
	}
	got, err = m.Call(context.Background(), "add", int64(4), int64(5))
	if err != nil || got.Value != int64(9) {
		t.Fatalf("restart = %#v, %v", got, err)
	}
}

func TestPrepareAggregationAliasesAndIdentity(t *testing.T) {
	blocks := []Block{{Language: "python", Alias: "py", Source: "def a(): return 1\n"}, {Language: "PYTHON", Alias: "py", Source: "def b(): return 2\n"}}
	a, err := Prepare(context.Background(), blocks, map[string]Analyzer{"python": Python{}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Prepare(context.Background(), blocks, map[string]Analyzer{"python": Python{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(a[0].Exports) != 2 || a[0].ID != b[0].ID || a[0].Alias != "py" {
		t.Fatalf("plans = %#v %#v", a, b)
	}
	blocks[1].Alias = "other"
	if _, err := Prepare(context.Background(), blocks, map[string]Analyzer{"python": Python{}}); err == nil {
		t.Fatal("inconsistent aliases accepted")
	}
}
