package polyglot

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func testTypeScript(t *testing.T) TypeScript {
	t.Helper()
	module := os.Getenv("BASHPP_TYPESCRIPT_MODULE")
	if module == "" {
		t.Skip("set BASHPP_TYPESCRIPT_MODULE to an official TypeScript compiler module")
	}
	return TypeScript{CompilerModule: module}
}

func TestTypeScriptAnalyzeAndCall(t *testing.T) {
	ts := testTypeScript(t)
	source := `
interface Pair { left: number; right: number }
type Label = string
export function add(a: number, b: number): number { console.log("typescript"); return a + b }
function label(value: Label): string { return value.toUpperCase() }
function dynamic(pair: Pair): Pair { return {left: pair.left + 1, right: pair.right + 1} }
`
	plans, err := Prepare(context.Background(), []Block{{Language: "ts", Source: source}}, map[string]Analyzer{"typescript": ts})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Language != "typescript" || plans[0].Artifact == "" || len(plans[0].Exports) != 3 {
		t.Fatalf("unexpected plan: %+v", plans)
	}
	module := Start(plans[0], ts)
	defer module.Close()
	result, err := module.Call(context.Background(), "add", float64(20), float64(22))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != float64(42) || result.Stdout != "typescript\n" {
		t.Fatalf("result = %#v", result)
	}
	result, err = module.Call(context.Background(), "label", "hello")
	if err != nil || result.Value != "HELLO" {
		t.Fatalf("label = %#v, %v", result, err)
	}
	result, err = module.Call(context.Background(), "dynamic", map[string]any{"left": float64(1), "right": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Value.(map[string]any)["left"]; got != int64(2) {
		t.Fatalf("dynamic result = %#v", result.Value)
	}
}

func TestTypeScriptDiagnosticsAndRuntime(t *testing.T) {
	ts := testTypeScript(t)
	for _, source := range []string{
		`import {x} from "./x"; export function f() { return x }`,
		`const initialized = 1; function f() { return initialized }`,
		`function broken(: number): number { return 1 }`,
	} {
		if _, _, err := ts.AnalyzeArtifact(context.Background(), source); err == nil || !strings.Contains(err.Error(), "<bash++ typescript>") {
			t.Fatalf("AnalyzeArtifact(%q) error = %v", source, err)
		}
	}
	exports, artifact, err := ts.AnalyzeArtifact(context.Background(), `function fail(): void { console.error("before"); throw new Error("boom") }`)
	if err != nil {
		t.Fatal(err)
	}
	module := Start(Plan{Language: "typescript", Artifact: artifact, Exports: exports}, ts)
	defer module.Close()
	result, err := module.Call(context.Background(), "fail")
	if err == nil || !strings.Contains(err.Error(), "boom") || result.Stderr != "before\n" {
		t.Fatalf("fail = %#v, %v", result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	exports, artifact, err = ts.AnalyzeArtifact(context.Background(), `function spin(): void { while (true) {} } function ok(): string { return "restarted" }`)
	if err != nil {
		t.Fatal(err)
	}
	module = Start(Plan{Language: "typescript", Artifact: artifact, Exports: exports}, ts)
	defer module.Close()
	if _, err := module.Call(ctx, "spin"); err == nil {
		t.Fatal("spin was not cancelled")
	}
	result, err = module.Call(context.Background(), "ok")
	if err != nil || result.Value != "restarted" {
		t.Fatalf("restart = %#v, %v", result, err)
	}
}

func TestTypeScriptToolchainErrorsAndCanonicalName(t *testing.T) {
	missing := TypeScript{NodeCommand: t.TempDir() + "/missing-node"}
	if _, err := missing.Analyze(context.Background(), `function f(): number { return 1 }`); err == nil || !strings.Contains(err.Error(), "runtime unavailable") {
		t.Fatalf("missing Node error = %v", err)
	}
	available := testTypeScript(t)
	missingCompiler := available
	missingCompiler.CompilerModule = t.TempDir() + "/missing-typescript"
	if _, err := missingCompiler.Analyze(context.Background(), `function f(): number { return 1 }`); err == nil || !strings.Contains(err.Error(), "compiler module unavailable") {
		t.Fatalf("missing compiler error = %v", err)
	}
	plans, err := Prepare(context.Background(), []Block{
		{Language: "ts", Alias: "typed", Source: `function one(): number { return 1 }`},
		{Language: "TypeScript", Alias: "typed", Source: `function two(): number { return 2 }`},
	}, map[string]Analyzer{"typescript": available})
	if err != nil || len(plans) != 1 || len(plans[0].Exports) != 2 {
		t.Fatalf("canonical plan = %+v, %v", plans, err)
	}
}
