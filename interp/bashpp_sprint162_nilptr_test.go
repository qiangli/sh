package interp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

// sprint162NilPtrRun runs one Go source reproducer of the interp-nilptr lane
// through the interpreter and returns its combined output and run error.
func sprint162NilPtrRun(t *testing.T, mechanism, name string) (string, error) {
	t.Helper()
	root := filepath.Join("testdata", "sprint162", "interp-nilptr", mechanism)
	source, err := os.ReadFile(filepath.Join(root, name+".go"))
	if err != nil {
		t.Fatal(err)
	}
	program, err := gosource.Parse(strings.NewReader(string(source)), name+".go", gosource.Options{RunMain: true})
	if err != nil {
		t.Fatalf("%s: parse: %v", name, err)
	}
	var out strings.Builder
	runner, err := New(Lang(syntax.LangBashPP), StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), program.File)
	return out.String(), err
}

// sprint162NilPtrExpect runs a reproducer and compares its output with the
// sibling .expected file byte for byte.
func sprint162NilPtrExpect(t *testing.T, mechanism, name string, wantErr bool) {
	t.Helper()
	got, err := sprint162NilPtrRun(t, mechanism, name)
	if (err != nil) != wantErr {
		t.Fatalf("%s/%s: run error %v, want error %v; output=%q", mechanism, name, err, wantErr, got)
	}
	want, readErr := os.ReadFile(filepath.Join("testdata", "sprint162", "interp-nilptr", mechanism, name+".expected"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got != string(want) {
		t.Fatalf("%s/%s: output\n%s\nwant\n%s", mechanism, name, got, string(want))
	}
}

func TestBashPPSprint162NilDereferencePanics(t *testing.T) {
	sprint162NilPtrExpect(t, "nilderef", "deref_recover", false)
	sprint162NilPtrExpect(t, "nilderef", "deref_order", false)
}

func TestBashPPSprint162RangePointerArray(t *testing.T) {
	sprint162NilPtrExpect(t, "rangeptr", "range_pointer_array", false)
	sprint162NilPtrExpect(t, "rangeptr", "index_pointer_array", false)
}

// An unrecovered nil dereference terminates the program as Go does: status 2,
// the runtime error reported, and nothing after the fault runs.
func TestBashPPSprint162NilDereferenceUnrecovered(t *testing.T) {
	got, err := sprint162NilPtrRun(t, "nilderef", "deref_unrecovered_negative")
	if err == nil {
		t.Fatalf("unrecovered fault did not terminate: output=%q", got)
	}
	if status, ok := IsExitStatus(err); !ok || status != 2 {
		t.Fatalf("status %v, want 2; output=%q", err, got)
	}
	if !strings.HasPrefix(got, "before\npanic: runtime error: invalid memory address or nil pointer dereference\n") || strings.Contains(got, "unreachable") {
		t.Fatalf("output %q", got)
	}
}
