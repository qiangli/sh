package polyglot

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCSharpLanguageRow(t *testing.T) {
	for _, spelling := range []string{"csharp", "cs", "CS"} {
		if got := CanonicalLanguage(spelling); got != "csharp" {
			t.Fatalf("CanonicalLanguage(%q) = %q", spelling, got)
		}
	}
	row, ok := LookupLanguage("cs")
	if !ok || !row.NeedsEnvironment || !row.LineDirectives || row.LoweredRuntime == nil {
		t.Fatalf("csharp row = %#v", row)
	}
	if got := row.LoweredRuntime("p.", "nil"); got != "p.polyglot.CSharp{Environment:nil}" {
		t.Fatalf("lowered runtime = %q", got)
	}
}

// C# resolves the same pwsh as the PowerShell row: one pinned archive serves
// both fences, and there is no project manifest.
func TestDiscoverCSharpEnvironmentUsesPwsh(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	pwsh := filepath.Join(bin, "pwsh")
	writeEnvironmentFile(t, pwsh, "fixture pwsh")
	plan, err := DiscoverEnvironment(EnvironmentRequest{
		Source: filepath.Join(root, "program.bpp"), Language: "cs", Environ: []string{"PATH=" + bin},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := canonicalExecutable(pwsh, nil)
	if plan.Language != "csharp" || plan.Runtime != "pwsh" || plan.Executable != canonical || len(plan.ResolutionFiles) != 0 {
		t.Fatalf("plan = %#v", plan)
	}
	_, err = DiscoverEnvironment(EnvironmentRequest{
		Source: filepath.Join(root, "program.bpp"), Language: "csharp", Environ: []string{"PATH="},
	})
	if err == nil || !strings.Contains(err.Error(), "C# runtime (pinned PowerShell) unavailable") {
		t.Fatalf("unavailable error = %v", err)
	}
}

// The generated unit keeps leading using lines before a generated namespace,
// wraps the members in a static class, and maps every line back to the fence.
func TestCSharpGenerateMapsFenceLines(t *testing.T) {
	source := aggregateSource("csharp", []Block{
		{Language: "csharp", Filename: `C:\work\flow "a".bsh`, Line: 4, Source: "using System;\nusing System.Linq;\n\npublic static long Square(long x) => x * x;\n"},
		{Language: "csharp", Filename: "flow.bsh", Line: 20, Source: "using static System.Math;\npublic static double Root(double x) => Sqrt(x);\n"},
	})
	generated, typeName, err := csharpGenerate(source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(typeName, "BashPP.CSharp.M") || !strings.HasSuffix(typeName, ".Fence") {
		t.Fatalf("type name = %q", typeName)
	}
	want := []string{
		`#line 4 "C:\work\flow 'a'.bsh"`,
		"using System;",
		"using System.Linq;",
		"",
		`#line 20 "flow.bsh"`,
		"using static System.Math;",
		`#line 7 "C:\work\flow 'a'.bsh"`,
		"namespace " + strings.TrimSuffix(typeName, ".Fence") + " { public static class Fence {",
		`#line 7 "C:\work\flow 'a'.bsh"`,
		"public static long Square(long x) => x * x;",
		`#line 21 "flow.bsh"`,
		"public static double Root(double x) => Sqrt(x);",
		`#line 21 "flow.bsh"`,
		"} }",
	}
	if got := strings.Split(strings.TrimSuffix(generated, "\n"), "\n"); !slices.Equal(got, want) {
		t.Fatalf("generated:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// The namespace is per source: another fence gets another type.
	_, other, err := csharpGenerate("public static int One() => 1;\n")
	if err != nil || other == typeName {
		t.Fatalf("namespace not per source: %q %q %v", other, typeName, err)
	}
}

func TestCSharpRefusesPackageDeclarations(t *testing.T) {
	for _, directive := range []string{`#:package Newtonsoft.Json@13.0.3`, `#r "nuget: Newtonsoft.Json, 13.0.3"`} {
		source := aggregateSource("csharp", []Block{{Language: "csharp", Filename: "flow.bsh", Line: 3, Source: "using System;\n" + directive + "\npublic static int One() => 1;\n"}})
		_, _, err := csharpGenerate(source)
		if err == nil || !strings.Contains(err.Error(), "flow.bsh:4:") || !strings.Contains(err.Error(), "NuGet") || !strings.Contains(err.Error(), CSharpNuGetFollowUp) {
			t.Fatalf("%s: error = %v", directive, err)
		}
	}
}

func cSharpRuntime(t *testing.T) CSharp {
	t.Helper()
	return CSharp{Command: requirePowerShell(t), CacheDir: t.TempDir()}
}

func cSharpPlan(t *testing.T, runtime CSharp, source string) Plan {
	t.Helper()
	plans, err := Prepare(context.Background(), []Block{{Language: "csharp", Filename: "flow.bsh", Line: 1, Source: source}}, map[string]Analyzer{"csharp": runtime})
	if err != nil {
		t.Fatal(err)
	}
	return plans[0]
}

const cSharpFixture = `using System;
using System.Collections.Generic;
using System.Linq;

public static long Square(long x) => x * x;
public static string Greet(string name) { Console.WriteLine("greeting " + name); return "hello " + name; }
public static double Half(double x) => x / 2;
public static bool Even(int n) => n % 2 == 0;
public static byte[] Bang(byte[] data) => data.Concat(new byte[] { 33 }).ToArray();
public static long[] Range(int n) => Enumerable.Range(1, n).Select(i => (long)i).ToArray();
public static Point Make(int x, string label) => new Point { X = x, Label = label };
public static int Fail(string why) => throw new InvalidOperationException(why);
public static int Count() => ++calls;
public static void Nothing() { }
static int calls;
static int Hidden() => 0;
public class Point { public int X { get; set; } public string Label = ""; }
`

func TestCSharpAnalyzeExportsByReflection(t *testing.T) {
	plan := cSharpPlan(t, cSharpRuntime(t), cSharpFixture)
	var names []string
	byName := map[string]Signature{}
	for _, e := range plan.Exports {
		names = append(names, e.Name)
		byName[e.Name] = e.Signature
	}
	if want := []string{"Square", "Greet", "Half", "Even", "Bang", "Range", "Make", "Fail", "Count", "Nothing"}; !slices.Equal(names, want) {
		t.Fatalf("exports = %v, want %v", names, want)
	}
	for name, want := range map[string]string{
		"Square": "int->int", "Greet": "string->string", "Half": "float64->float64", "Even": "int->bool",
		"Bang": "bytes->bytes", "Range": "int->object", "Make": "int,string->object", "Nothing": "->",
	} {
		sig := byName[name]
		if got := strings.Join(sig.Params, ",") + "->" + strings.Join(sig.Results, ","); got != want || sig.Dynamic {
			t.Fatalf("%s signature = %s (dynamic %v), want %s", name, got, sig.Dynamic, want)
		}
	}
}

func TestCSharpTypedCallsErrorsAndState(t *testing.T) {
	runtime := cSharpRuntime(t)
	plan := cSharpPlan(t, runtime, cSharpFixture)
	m := Start(plan, runtime)
	defer m.Close()
	ctx := context.Background()

	if got, err := m.Call(ctx, "Square", int64(6)); err != nil || got.Value != int64(36) {
		t.Fatalf("Square = %#v, %v", got, err)
	}
	greet, err := m.Call(ctx, "Greet", "bashy")
	if err != nil || greet.Value != "hello bashy" || greet.Stdout != "greeting bashy\n" {
		t.Fatalf("Greet = %#v, %v", greet, err)
	}
	if got, err := m.Call(ctx, "Half", 5.0); err != nil || got.Value != 2.5 {
		t.Fatalf("Half = %#v, %v", got, err)
	}
	if got, err := m.Call(ctx, "Even", int64(4)); err != nil || got.Value != true {
		t.Fatalf("Even = %#v, %v", got, err)
	}
	if got, err := m.Call(ctx, "Bang", []byte("hi")); err != nil || string(got.Value.([]byte)) != "hi!" {
		t.Fatalf("Bang = %#v, %v", got, err)
	}
	if got, err := m.Call(ctx, "Range", int64(3)); err != nil || !slices.Equal(got.Value.([]any), []any{int64(1), int64(2), int64(3)}) {
		t.Fatalf("Range = %#v, %v", got, err)
	}
	point, err := m.Call(ctx, "Make", int64(7), "seven")
	if err != nil {
		t.Fatal(err)
	}
	if obj, ok := point.Value.(map[string]any); !ok || obj["X"] != int64(7) || obj["Label"] != "seven" {
		t.Fatalf("Make = %#v", point.Value)
	}
	if got, err := m.Call(ctx, "Nothing"); err != nil || got.Value != nil {
		t.Fatalf("Nothing = %#v, %v", got, err)
	}
	_, err = m.Call(ctx, "Fail", "nope")
	detail, ok := ForeignErrorDetail(err)
	if !ok || detail.Code != "InvalidOperationException" || detail.Message != "nope" {
		t.Fatalf("Fail error = %v (%#v)", err, detail)
	}
	// Static state persists in the one worker across calls, including after a
	// failed call.
	for want := int64(1); want <= 2; want++ {
		if got, err := m.Call(ctx, "Count"); err != nil || got.Value != want {
			t.Fatalf("Count = %#v, %v; want %d", got, err, want)
		}
	}
	if _, err := m.Call(ctx, "Square", "x"); err == nil {
		t.Fatal("Square(\"x\") succeeded")
	}
	if _, err := m.Call(ctx, "Hidden"); err == nil || !strings.Contains(err.Error(), "no C# method Hidden") {
		t.Fatalf("Hidden error = %v", err)
	}
}

// Compiler errors name the script file and the fence's own lines — never the
// generated wrapper's — and leave no cache entry behind.
func TestCSharpCompileErrorsUseFenceLines(t *testing.T) {
	runtime := cSharpRuntime(t)
	_, err := Prepare(context.Background(), []Block{{Language: "csharp", Filename: "flow.bsh", Line: 10, Source: "using System;\n\npublic static int Ok() => 1;\npublic static int Bad() => missing + 1;\n"}}, map[string]Analyzer{"csharp": runtime})
	if err == nil || !strings.Contains(err.Error(), "flow.bsh:13:") || !strings.Contains(err.Error(), "CS0103") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("compile error = %v", err)
	}
	_, err = Prepare(context.Background(), []Block{{Language: "csharp", Filename: "flow.bsh", Line: 10, Source: "using Nope.Missing;\npublic static int Ok() => 1;\n"}}, map[string]Analyzer{"csharp": runtime})
	if err == nil || !strings.Contains(err.Error(), "flow.bsh:10:") {
		t.Fatalf("using error = %v", err)
	}
	entries, _ := os.ReadDir(runtime.CacheDir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".dll") || strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("failed compile left %s in the cache", entry.Name())
		}
	}
	// An overload compiles but is refused at prepare: an export is one name
	// with one signature.
	_, err = Prepare(context.Background(), []Block{{Language: "csharp", Filename: "flow.bsh", Line: 10, Source: "public static int A(int x) => x;\npublic static int A(string x) => 1;\n"}}, map[string]Analyzer{"csharp": runtime})
	if err == nil || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("overload error = %v", err)
	}
}

// An unchanged fence on an unchanged toolchain is analyzed and loaded from the
// cache: no second compile, and the cached assembly is the one that runs.
func TestCSharpSecondRunDoesNotRecompile(t *testing.T) {
	runtime := cSharpRuntime(t)
	source := "public static long Square(long x) => x * x;\n"
	before := csharpCompiles.Load()
	plan := cSharpPlan(t, runtime, source)
	if csharpCompiles.Load() != before+1 {
		t.Fatalf("first run compiled %d times", csharpCompiles.Load()-before)
	}
	unit, err := runtime.unit(plan.Source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(unit.assembly)
	if err != nil {
		t.Fatalf("no cached assembly: %v", err)
	}
	m := Start(plan, runtime)
	if got, err := m.Call(context.Background(), "Square", int64(5)); err != nil || got.Value != int64(25) {
		t.Fatalf("first Square = %#v, %v", got, err)
	}
	m.Close()

	second := cSharpPlan(t, runtime, source)
	if csharpCompiles.Load() != before+1 {
		t.Fatal("second run recompiled")
	}
	m = Start(second, runtime)
	defer m.Close()
	if got, err := m.Call(context.Background(), "Square", int64(6)); err != nil || got.Value != int64(36) {
		t.Fatalf("second Square = %#v, %v", got, err)
	}
	after, err := os.Stat(unit.assembly)
	if err != nil || !after.ModTime().Equal(info.ModTime()) {
		t.Fatalf("cached assembly rewritten: %v", err)
	}
	// A changed body is a different key and compiles again.
	cSharpPlan(t, runtime, "public static long Square(long x) => x * x * 1;\n")
	if csharpCompiles.Load() != before+2 {
		t.Fatal("changed fence did not compile")
	}
}

// A cache entry that disappears between prepare and the first call is
// recompiled by the worker rather than failing the call.
func TestCSharpWorkerRecompilesMissingAssembly(t *testing.T) {
	runtime := cSharpRuntime(t)
	plan := cSharpPlan(t, runtime, "public static string Id(string s) => s;\n")
	unit, err := runtime.unit(plan.Source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(unit.assembly); err != nil {
		t.Fatal(err)
	}
	m := Start(plan, runtime)
	defer m.Close()
	if got, err := m.Call(context.Background(), "Id", "x"); err != nil || got.Value != "x" {
		t.Fatalf("Id = %#v, %v", got, err)
	}
	if _, err := os.Stat(unit.assembly); err != nil {
		t.Fatalf("worker did not restore the cache entry: %v", err)
	}
}
