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
// compiler import-path phase. Exact list equality is intentionally used: the
// negative checks cover a missing, extra, duplicate, wrong-line, wrong-wording
// and unexpected-success result, the same properties errorCheck enforces.
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
		`reject.go:3:10: non-canonical import path "unicode//utf8" (should be "unicode/utf8")`,
	}
	got := diagnostics("reject.go", read("reject.go.src"))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("errorCheck comparison mismatch\ngot: %q\nwant: %q", got, want)
	}

	negatives := map[string][]string{
		"missing":            got[1:],
		"extra":              append(append([]string{}, got...), "reject.go:5:1: extra"),
		"duplicate":          append(append([]string{}, got...), got[0]),
		"wrong-line":         {strings.Replace(got[0], ":3:", ":2:", 1)},
		"wrong-wording":      {strings.Replace(got[0], "non-canonical", "canonical", 1)},
		"unexpected-success": nil,
	}
	for name, candidate := range negatives {
		if reflect.DeepEqual(candidate, want) {
			t.Fatalf("negative %s was accepted", name)
		}
	}
	// gc exempts local names ("./x", "../x", "/x") from the canonical-form
	// check (noder/import.go islocalname); a relative import must reach the
	// importer, never this diagnostic.
	for _, local := range []string{"./b", "../b", "/abs/b"} {
		src := []byte("package p\n\nimport _ \"" + local + "\"\n")
		for _, d := range diagnostics("local.go", src) {
			if strings.Contains(d, "non-canonical import path") {
				t.Fatalf("local import %q was canonicalised: %q", local, d)
			}
		}
	}
	if got := diagnostics("empty.go", read("empty.go.src")); !reflect.DeepEqual(got, []string{"empty.go:3:8: invalid import path (empty string)"}) {
		t.Fatalf("empty import path differs from gc: %q", got)
	}
}
