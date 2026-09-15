package polyglot

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestRustNativeArtifactCallsAndDiagnostics(t *testing.T) {
	rustc, err := exec.LookPath("rustc")
	if err != nil {
		t.Skip("rustc unavailable")
	}
	runtime := Rust{Command: rustc}
	plans, err := Prepare(context.Background(), []Block{{Language: "rs", Source: `
pub fn add(a: i64, b: i64) -> i64 { println!("rust"); a + b }
pub fn greet(name: &str) -> String { format!("hello {name}") }
pub fn invert(value: bool) -> bool { !value }
pub fn blob(value: Vec<u8>) -> Vec<u8> { [value, vec![0xff]].concat() }
pub fn checked(value: i64) -> Result<i64, String> {
    if value < 0 { Err("negative".into()) } else { Ok(value) }
}
`}}, map[string]Analyzer{"rust": runtime})
	if err != nil {
		t.Fatal(err)
	}
	if got := plans[0]; got.Language != "rust" || len(got.Exports) != 5 || len(got.Artifact) == 0 {
		t.Fatalf("plan = %#v", got)
	}
	module := Start(plans[0], runtime)
	defer module.Close()
	result, err := module.Call(context.Background(), "add", int64(20), int64(22))
	if err != nil || result.Value != int64(42) || result.Stdout != "rust\n" {
		t.Fatalf("add = %#v, %v", result, err)
	}
	result, err = module.Call(context.Background(), "greet", "wor\x00ld")
	if err != nil || result.Value != "hello wor\x00ld" {
		t.Fatalf("greet = %#v, %v", result, err)
	}
	result, err = module.Call(context.Background(), "invert", true)
	if err != nil || result.Value != false {
		t.Fatalf("invert = %#v, %v", result, err)
	}
	result, err = module.Call(context.Background(), "blob", []byte{0, 1})
	if err != nil || string(result.Value.([]byte)) != string([]byte{0, 1, 0xff}) {
		t.Fatalf("blob = %#v, %v", result, err)
	}
	if _, err := module.Call(context.Background(), "checked", int64(-1)); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("checked error = %v", err)
	}
}

func TestRustRejectsUnsupportedSurface(t *testing.T) {
	for _, source := range []string{
		"pub fn borrowed() -> &str { \"x\" }\n",
		"pub fn tuple() -> (i64, i64) { (1, 2) }\n",
	} {
		if _, _, err := (Rust{}).AnalyzeArtifact(context.Background(), source); err == nil {
			t.Fatalf("accepted %q", source)
		}
	}
}
