package lower_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/lower"
)

// TestGoSourceLibraryLocalConstImport drives the outside-corpus reproducer of
// the compiled package-fidelity residue: a function-local constant whose
// initializer selects through an imported package is the file's only use of
// that import. Folding it erases the use and leaves the generated import
// unused, so the local declaration must retain its source form.
func TestGoSourceLibraryLocalConstImport(t *testing.T) {
	dir := filepath.Join("testdata", "sprint165", "package-fidelity", "local-const-import")
	names := []string{"word.go", "limit.go", "plain.go"}
	var sources []gosource.Source
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, gosource.Source{Name: name, Data: data})
	}
	program, err := gosource.Load(sources, gosource.Options{PreserveNativeInit: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := lower.Compile(program.File, lower.Options{Package: program.Package, Library: true, Importer: program.Importer})
	if err != nil {
		t.Fatalf("library transpile refused the package: %v", err)
	}
	if len(result.Files) != len(names) {
		t.Fatalf("emitted %d files, want %d", len(result.Files), len(names))
	}

	generated := map[string]string{}
	for _, output := range result.Files {
		generated[output.Name] = string(output.Source)
	}
	if got := generated["word.go"]; !strings.Contains(got, "unsafe.Sizeof(uintptr(0)) == 8") {
		t.Errorf("word.go folded the unsafe.Sizeof constant\n--- generated\n%s", got)
	}
	if got := generated["limit.go"]; !strings.Contains(got, "math.MaxInt8") {
		t.Errorf("limit.go folded the math.MaxInt8 constant\n--- generated\n%s", got)
	}
	plain := generated["plain.go"]
	if strings.Contains(plain, "import") {
		t.Errorf("plain.go gained an import\n--- generated\n%s", plain)
	}
	// Sprint 171 (w3-fidelity): a Go-only input lowers to itself — the
	// written initializer is the compiled carrier even when it needs no
	// import, so gc evaluates `len("abc")` exactly as it did the original
	// (folding to `3` was the Sprint 169 boundary this supersedes).
	if !strings.Contains(plain, `const width = len("abc")`) || strings.Contains(plain, "const width = 3") {
		t.Errorf("plain.go does not keep its written import-free constant\n--- generated\n%s", plain)
	}

	buildDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(buildDir, "go.mod"), []byte("module example/fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, output := range result.Files {
		if err := os.WriteFile(filepath.Join(buildDir, output.Name), []byte(generated[output.Name]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = buildDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build refused the generated package: %v\n%s", err, out.Bytes())
	}
}

func TestGoSourceLibraryUnusedImportStaysRefused(t *testing.T) {
	source := gosource.Source{Name: "unused.go", Data: []byte("package fixture\n\nimport \"unsafe\"\n\nfunc F() int { return 4 }\n")}
	_, err := gosource.Load([]gosource.Source{source}, gosource.Options{PreserveNativeInit: true})
	if err == nil {
		t.Fatal("an unused import was accepted")
	}
	if !strings.Contains(err.Error(), `"unsafe" imported and not used`) {
		t.Fatalf("unexpected diagnostic: %v", err)
	}
}

func TestGoSourceLibraryLocalConstIotaBound(t *testing.T) {
	data := []byte(`package fixture

import "unsafe"

func Sizes() int {
	const (
		first  = iota + int(unsafe.Sizeof(int(0)))
		second = iota + int(unsafe.Sizeof(int(0)))
	)
	return first + second
}
`)
	program, err := gosource.Load([]gosource.Source{{Name: "sizes.go", Data: data}}, gosource.Options{PreserveNativeInit: true})
	if err != nil {
		t.Fatal(err)
	}
	// Sprint 171 (w3-fidelity) decided the group-carrier shape: a native
	// const group is emitted as its written initializers, so iota keeps its
	// per-line value and the import stays used. Sprint 169 pinned the
	// refusal this replaces; the generated unit must now build and compute
	// what the original does (first=8, second=9 on a 64-bit target).
	result, err := lower.Compile(program.File, lower.Options{Package: program.Package, Library: true, Importer: program.Importer})
	if err != nil {
		t.Fatalf("the iota group no longer lowers: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("emitted %d files, want 1", len(result.Files))
	}
	generated := string(result.Files[0].Source)
	for _, want := range []string{"first  = iota + int(unsafe.Sizeof(int(0)))", "second = iota + int(unsafe.Sizeof(int(0)))", `import "unsafe"`} {
		if !strings.Contains(generated, want) {
			t.Fatalf("generated unit lost %q:\n%s", want, generated)
		}
	}
}
