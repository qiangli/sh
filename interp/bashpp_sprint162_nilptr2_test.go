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

// sprint162NilPtr2Run runs one Go source reproducer of the interp-nilptr-2
// lane through the interpreter and returns its combined output and run error.
func sprint162NilPtr2Run(t *testing.T, mechanism, name string) (string, error) {
	t.Helper()
	root := filepath.Join("testdata", "sprint162", "interp-nilptr-2", mechanism)
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

// sprint162NilPtr2Expect runs a reproducer and compares its output with the
// sibling .expected file byte for byte.
func sprint162NilPtr2Expect(t *testing.T, mechanism, name string, wantErr bool) {
	t.Helper()
	got, err := sprint162NilPtr2Run(t, mechanism, name)
	if (err != nil) != wantErr {
		t.Fatalf("%s/%s: run error %v, want error %v; output=%q", mechanism, name, err, wantErr, got)
	}
	want, readErr := os.ReadFile(filepath.Join("testdata", "sprint162", "interp-nilptr-2", mechanism, name+".expected"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got != string(want) {
		t.Fatalf("%s/%s: output\n%s\nwant\n%s", mechanism, name, got, string(want))
	}
}

// sprint162NilPtr2Refused asserts the checker still refuses a .go.src source
// with a diagnostic containing want.
func sprint162NilPtr2Refused(t *testing.T, mechanism, name, want string) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", "sprint162", "interp-nilptr-2", mechanism, name+".go.src"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gosource.Parse(strings.NewReader(string(source)), name+".go", gosource.Options{RunMain: true}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s/%s: accepted or wrongly refused: %v", mechanism, name, err)
	}
}

// A typed float variable reads back as a float whatever text its cell stores.
func TestBashPPSprint162TypedFloatStorage(t *testing.T) {
	sprint162NilPtr2Expect(t, "typedfloat", "typed_float", false)
	sprint162NilPtr2Refused(t, "typedfloat", "typed_float_negative", "operator - not defined on")
}

// A nil function value is an argument to a func-typed parameter; a call
// through it faults. A closure of the wrong signature stays refused.
func TestBashPPSprint162NilFuncArgument(t *testing.T) {
	sprint162NilPtr2Expect(t, "nilfunc", "nil_func_argument", false)
	sprint162NilPtr2Refused(t, "nilfunc", "nil_func_argument_negative", "cannot use")
}
