package polyglot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextRowMaterializesAndRunsTool(t *testing.T) {
	saved := ToolResolver
	ToolResolver = func(name string) ([]string, string, error) {
		if name != "fake-tool" {
			t.Fatalf("resolved %q", name)
		}
		return []string{"/bin/sh", "-c", `printf '%s|%s|%s|%s|%s\n' "$0" "$(basename "$1")" "$(cat "$1" | tr -d '\n')" "$2" "$BASHPP_FENCE_TYPE"`}, "fake", nil
	}
	t.Cleanup(func() { ToolResolver = saved })
	text := Text{Type: "fakecfg", FileName: "fake.cfg", Tool: "fake-tool", Verbs: []Verb{
		{Name: "show", Args: []string{"show", "{file}"}},
		{Name: "apply", Args: []string{"apply", "{file}"}, Effect: "world"},
	}}
	ctx := context.Background()
	plans, err := Prepare(ctx, []Block{{Language: "fakecfg", Alias: "c", Source: "k=v\n"}}, map[string]Analyzer{"fakecfg": text})
	if err != nil {
		t.Fatal(err)
	}
	plan := plans[0]
	if len(plan.Exports) != 2 || plan.Exports[1].Effect != "world" || !plan.Exports[0].Signature.Variadic {
		t.Fatalf("exports = %+v", plan.Exports)
	}
	module := Start(plan, text)
	result, err := module.Call(ctx, "show", "extra")
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "show|fake.cfg|k=v|extra|fakecfg" {
		t.Fatalf("value = %q", result.Value)
	}
	file := filepath.Join(textRoot(), plan.ID, "fake.cfg")
	if data, err := os.ReadFile(file); err != nil || string(data) != "k=v\n" {
		t.Fatalf("materialized %s: %q, %v", file, data, err)
	}
	if _, err := module.Call(ctx, "destroy"); err == nil || !strings.Contains(err.Error(), "no method destroy") {
		t.Fatalf("undeclared verb: %v", err)
	}
	// A failing tool is the call's error, with stderr carried.
	ToolResolver = func(string) ([]string, string, error) {
		return []string{"/bin/sh", "-c", "echo boom >&2; exit 3"}, "", nil
	}
	if result, err := module.Call(ctx, "apply"); err == nil || !strings.Contains(err.Error(), "exited 3") || result.Stderr != "boom\n" {
		t.Fatalf("failing tool: %v %+v", err, result)
	}
}

func TestRunnerFenceDeclaresMethods(t *testing.T) {
	var seen [][]string
	fence := RunnerFence{Type: "notes", Runner: "tally", Invoke: func(_ context.Context, argv []string) (string, error) {
		seen = append(seen, argv)
		switch argv[0] {
		case MethodsVerb:
			return "{\"name\":\"count\"}\n\n{\"name\":\"apply\",\"effect\":\"world\",\"signature\":{\"params\":[\"string\"],\"results\":[\"string\"]}}\n", nil
		case "count":
			data, err := os.ReadFile(argv[1])
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(strings.Repeat("x", strings.Count(string(data), "\n"))), nil
		}
		return "", nil
	}}
	ctx := context.Background()
	plans, err := Prepare(ctx, []Block{{Language: "notes", Alias: "n", Runner: "tally", Source: "a\nb\n"}}, map[string]Analyzer{"notes": fence})
	if err != nil {
		t.Fatal(err)
	}
	plan := plans[0]
	if plan.Runner != "tally" || len(plan.Exports) != 2 || plan.Exports[1].Effect != "world" || plan.Exports[1].Signature.Variadic {
		t.Fatalf("plan = %+v", plan)
	}
	module := Start(plan, fence)
	result, err := module.Call(ctx, "count")
	if err != nil || result.Value != "xx" {
		t.Fatalf("count = %v, %v", result, err)
	}
	if _, err := module.Call(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "declares no method nope") {
		t.Fatalf("undeclared: %v", err)
	}
	if seen[0][0] != MethodsVerb || filepath.Base(seen[0][1]) != "fence.notes" || filepath.Base(seen[1][1]) != "fence.notes" {
		t.Fatalf("argv seen = %v", seen)
	}
	// Runner and alias must agree across blocks of one type.
	if _, err := Prepare(ctx, []Block{{Language: "notes", Alias: "n", Runner: "tally"}, {Language: "notes", Alias: "n", Runner: "other"}}, map[string]Analyzer{"notes": fence}); err == nil || !strings.Contains(err.Error(), "inconsistent runners") {
		t.Fatalf("inconsistent runners: %v", err)
	}
}

func TestParseMethods(t *testing.T) {
	for answer, want := range map[string]string{
		"":                                   "declared no methods",
		"\n  \n":                             "declared no methods",
		"not json":                           "not a method object",
		`{"signature":{}}`:                   "without a name",
		`{"name":"methods"}`:                 "reserved",
		"{\"name\":\"a\"}\n{\"name\":\"a\"}": "declared twice",
	} {
		if _, err := ParseMethods("r", answer); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseMethods(%q) = %v, want %q", answer, err, want)
		}
	}
	exports, err := ParseMethods("r", `{"name":"plan"}`)
	if err != nil || len(exports) != 1 || exports[0].Signature.Params[0] != "string" || !exports[0].Signature.Variadic || exports[0].Signature.Results[0] != "string" {
		t.Fatalf("default signature: %+v, %v", exports, err)
	}
}

func TestMaterializeTextRejectsPaths(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := materializeText("k", name, "x"); err == nil {
			t.Errorf("file name %q accepted", name)
		}
	}
}
