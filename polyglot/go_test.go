package polyglot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoArtifactUsesModuleOverlayAndBridgesValues(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "version"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.local/project\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "project.go"), []byte("package project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "version", "version.go"), []byte("package version\nconst Value = \"module\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := directoryNames(t, root)
	environment, err := DiscoverEnvironment(EnvironmentRequest{Source: filepath.Join(root, "dag.md"), Language: "go"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := Go{Environment: &environment}
	plans, err := Prepare(context.Background(), []Block{{Language: "go", Source: `
import "example.local/project/version"
import "fmt"
func Add(a int64, b int64) int64 { fmt.Println(version.Value); return a+b }
func Blob(value []byte) []byte { return append(value, 0xff) }
func Checked(value int64) (int64, error) { if value < 0 { return 0, fmt.Errorf("negative") }; return value, nil }
`}}, map[string]Analyzer{"go": runtime})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || len(plans[0].Exports) != 3 || plans[0].Artifact == "" {
		t.Fatalf("plan = %#v", plans)
	}
	if after := directoryNames(t, root); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("overlay build changed checkout: before=%v after=%v", before, after)
	}
	module := Start(plans[0], runtime)
	defer module.Close()
	result, err := module.Call(context.Background(), "Add", int64(20), int64(22))
	if err != nil || result.Value != int64(42) || result.Stdout != "module\n" {
		t.Fatalf("Add = %#v, %v", result, err)
	}
	result, err = module.Call(context.Background(), "Blob", []byte{0, 1})
	if err != nil || string(result.Value.([]byte)) != string([]byte{0, 1, 0xff}) {
		t.Fatalf("Blob = %#v, %v", result, err)
	}
	if _, err := module.Call(context.Background(), "Checked", int64(-1)); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("Checked error = %v", err)
	}
}

func TestGoRejectsUnsupportedSurface(t *testing.T) {
	for _, source := range []string{
		"var X = 1\nfunc Value() int { return X }\n",
		"func Values() (int, int) { return 1, 2 }\n",
		"func value() int { return 1 }\n",
		"func main() {}\nfunc Value() int { return 1 }\n",
	} {
		exports, err := analyzeGoExports(source)
		if err == nil && len(exports) != 0 {
			t.Fatalf("accepted %q as %#v", source, exports)
		}
	}
}

func directoryNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
