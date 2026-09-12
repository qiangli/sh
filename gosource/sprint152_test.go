package gosource_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower"
	"mvdan.cc/sh/v3/syntax"
)

// TestSprint152Converter runs one small out-of-corpus Go program per converter
// defect localized under testdata/sprint152/<mechanism>/. Each program is valid
// Go with deterministic output, so it is executed three ways that must agree:
// go run of the original, the Bash++ interpreter over the loaded program, and
// go run of the lowered Go emitted by lower.Compile. The lower.Compile step is
// what catches the LOWER-E* diagnostics these reductions were minted from.
func TestSprint152Converter(t *testing.T) {
	for _, tc := range []struct {
		mechanism, file string
	}{
		{"blank-target-paren", "blank-target-paren/blank_target_paren.go"},
		{"new-paren", "new-paren/new_paren.go"},
		{"const-defined-type", "const-defined-type/const_defined_type.go"},
	} {
		t.Run(tc.mechanism, func(t *testing.T) {
			path := filepath.Join("testdata", "sprint152", filepath.FromSlash(tc.file))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Base(tc.file)
			goOut, err := exec.Command("go", "run", path).Output()
			if err != nil {
				t.Fatalf("go run: %v", err)
			}
			program, err := gosource.Load([]gosource.Source{{Name: name, Data: data}}, gosource.Options{RunMain: true})
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &stdout, &stderr))
			if err != nil {
				t.Fatal(err)
			}
			if err := runner.Run(context.Background(), program.File); err != nil || stderr.Len() > 0 {
				t.Fatalf("interpreter: err=%v stderr=%q", err, stderr.String())
			}
			if !bytes.Equal(stdout.Bytes(), goOut) {
				t.Fatalf("stdout differs from go run\ninterpreter: %q\ngo run:      %q", stdout.Bytes(), goOut)
			}
			result, err := lower.Compile(program.File, lower.Options{Origin: name})
			if err != nil {
				t.Fatalf("lower: %v", err)
			}
			generated := filepath.Join(t.TempDir(), "generated.go")
			if err := os.WriteFile(generated, result.Source, 0o600); err != nil {
				t.Fatal(err)
			}
			loweredOut, err := exec.Command("go", "run", generated).Output()
			if err != nil {
				t.Fatalf("go run lowered program: %v\n%s", err, result.Source)
			}
			if !bytes.Equal(loweredOut, goOut) {
				t.Fatalf("lowered stdout differs from go run\nlowered: %q\ngo run:  %q", loweredOut, goOut)
			}
		})
	}
}

// TestSprint152SyntheticCallPosition pins the C5 converter half: the synthetic
// `main` wrapper's call to the renamed source main must carry no borrowed
// //line. The first declaration is an import, so the old borrowed position
// stamped a //line on the call attributing diagnostics to the import's line.
func TestSprint152SyntheticCallPosition(t *testing.T) {
	path := filepath.Join("testdata", "sprint152", "synthetic-main-call", "synthetic_main_call.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Base(path)
	program, err := gosource.Load([]gosource.Source{{Name: name, Data: data}}, gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := lower.Compile(program.File, lower.Options{Origin: name})
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	// Find the wrapper's call to the renamed source main and assert no //line
	// directive sits between `func main() {` and that call.
	lines := strings.Split(string(result.Source), "\n")
	wrapper := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "func main() {" {
			wrapper = i
			break
		}
	}
	if wrapper < 0 {
		t.Fatalf("no synthetic main wrapper in lowered source:\n%s", result.Source)
	}
	found := false
	for i := wrapper + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasSuffix(trimmed, "sourceMain()") {
			found = true
			break
		}
		if strings.HasPrefix(trimmed, "//line ") {
			t.Fatalf("synthetic main call borrows a position: %q precedes the sourceMain call\n%s", trimmed, result.Source)
		}
	}
	if !found {
		t.Fatalf("no sourceMain call inside the synthetic wrapper:\n%s", result.Source)
	}
}
