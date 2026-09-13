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

// bashPPSprint162Control4Run interprets one original Go program from the
// interp-control-4 reproducer tree and returns its combined output and the
// run error.
func bashPPSprint162Control4Run(t *testing.T, mechanism, name string) (string, error) {
	t.Helper()
	root := filepath.Join("testdata", "sprint162", "interp-control-4", mechanism)
	source, err := os.ReadFile(filepath.Join(root, name+".go"))
	if err != nil {
		t.Fatal(err)
	}
	program, err := gosource.Parse(strings.NewReader(string(source)), name+".go", gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runner, err := New(Lang(syntax.LangBashPP), StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), program.File)
	return out.String(), err
}

func bashPPSprint162Control4Expected(t *testing.T, mechanism, name string) string {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", "sprint162", "interp-control-4", mechanism, name+".expected"))
	if err != nil {
		t.Fatal(err)
	}
	return string(want)
}

// TestBashPPSprint162StructFieldRace: distinct fields of one shared struct
// written from two tasks is legal Go; the interpreter's struct storage must
// not turn it into a fatal map race. Run with -race to observe.
func TestBashPPSprint162StructFieldRace(t *testing.T) {
	for _, name := range []string{"fields", "select_fanin"} {
		for i := 0; i < 10; i++ {
			out, err := bashPPSprint162Control4Run(t, "struct_field_race", name)
			if err != nil {
				t.Fatalf("%s: run: %v, output=%q", name, err, out)
			}
			if want := bashPPSprint162Control4Expected(t, "struct_field_race", name); out != want {
				t.Fatalf("%s: output %q, want %q", name, out, want)
			}
		}
	}
}
