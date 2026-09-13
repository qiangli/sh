package gosource_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
)

// TestSprint162ImportPathDiagnostics is an outside-corpus reproducer for the
// compiler's import-path rules (noder/import.go resolveImportPath and
// openPackage), applied by the importer before any package map or on-disk
// lookup, so go/types renders them as "could not import P (E)" exactly as
// types2 renders them for gc and keeps diagnosing the file's later imports.
// The expected list is `go tool compile -p diag reject.go` verbatim. Exact
// list equality is intentionally used: the negative checks cover a missing,
// extra, duplicate, wrong-line, wrong-wording and unexpected-success result,
// the same properties errorCheck enforces.
func TestSprint162ImportPathDiagnostics(t *testing.T) {
	base := filepath.Join("testdata", "sprint162", "diag", "import-path")
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	diagnostics := func(name string, data []byte) []string {
		t.Helper()
		_, err := gosource.Load([]gosource.Source{{Name: name, Data: data}}, gosource.Options{})
		if err == nil {
			return nil
		}
		list, ok := err.(gosource.ErrorList)
		if !ok {
			t.Fatalf("diagnostics lost types: %T", err)
		}
		out := make([]string, len(list))
		for i, diagnostic := range list {
			out[i] = diagnostic.Error()
		}
		return out
	}

	if got := diagnostics("positive.go", read("positive.go")); got != nil {
		t.Fatalf("positive control rejected: %q", got)
	}
	want := []string{
		`reject.go:3:10: could not import unicode//utf8 (non-canonical import path "unicode//utf8" (should be "unicode/utf8"))`,
		`reject.go:4:10: could not import /abs/b (import path cannot be absolute path)`,
		`reject.go:5:10: could not import main (cannot import "main")`,
		`reject.go:6:8: invalid import path (empty string)`,
		`reject.go:7:10: invalid import path (invalid character U+003A ':')`,
	}
	got := diagnostics("reject.go", read("reject.go.src"))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("errorCheck comparison mismatch\ngot: %q\nwant: %q", got, want)
	}

	negatives := map[string][]string{
		"missing":            got[1:],
		"extra":              append(append([]string{}, got...), "reject.go:8:1: extra"),
		"duplicate":          append(append([]string{}, got...), got[0]),
		"wrong-line":         append([]string{strings.Replace(got[0], ":3:", ":2:", 1)}, got[1:]...),
		"wrong-wording":      append([]string{strings.Replace(got[0], "non-canonical", "canonical", 1)}, got[1:]...),
		"importer-wording":   append([]string{`reject.go:3:10: could not import unicode//utf8 (go list failed)`}, got[1:]...),
		"unexpected-success": nil,
	}
	for name, candidate := range negatives {
		if reflect.DeepEqual(candidate, want) {
			t.Fatalf("negative %s was accepted", name)
		}
	}
	// gc exempts local names ("./x", "../x") from the canonical-form check
	// (noder/import.go islocalname); a relative import must reach the
	// relative-import rule, never a compiler path diagnostic.
	for _, local := range []string{"./b", "../b", "./b//c"} {
		src := []byte("package p\n\nimport _ \"" + local + "\"\n")
		for _, d := range diagnostics("local.go", src) {
			if strings.Contains(d, "non-canonical import path") || strings.Contains(d, "absolute path") {
				t.Fatalf("local import %q got a compiler path diagnostic: %q", local, d)
			}
		}
	}
}
