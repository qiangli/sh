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

// sprint171RunBothModes loads path as an executing program and requires the
// interpreter's and the lowered program's stdout to be `go run`'s.
func sprint171RunBothModes(t *testing.T, path string) *gosource.Program {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	goOut, err := exec.Command("go", "run", path).Output()
	if err != nil {
		t.Fatalf("go run: %v", err)
	}
	program, err := gosource.Load([]gosource.Source{{Name: filepath.Base(path), Data: data}}, gosource.Options{RunMain: true})
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
	result, err := lower.Compile(program.File, lower.Options{Origin: filepath.Base(path)})
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
	return program
}

// TestSprint171TypeSwitchInit is an outside-corpus reproducer for a type
// switch with an init statement (`switch i := f(); v := i.(type)`), which
// the converter refused as an unsupported initializer. Go scopes the init to
// the switch, so it converts to a block holding the init and the switch —
// the shape lowering already gives an expression switch with an init. The
// program covers the scope (an outer name the init shadows is intact after
// the switch), a labeled break through the switch, a goto that re-enters
// the labeled switch so the init runs again, and an assignment init.
func TestSprint171TypeSwitchInit(t *testing.T) {
	program := sprint171RunBothModes(t, filepath.Join("testdata", "sprint171", "w2-imports", "typeswitch-init", "typeswitch_init.go"))

	// Tree shape: every type switch with an init is the last statement of a
	// block; the switch is labeled inside the block when only a break names
	// the label, and the block carries the label when a goto does.
	var labeled []string
	syntax.Walk(program.File, func(n syntax.Node) bool {
		l, ok := n.(*syntax.BashPPLabeled)
		if !ok || l.Stmt == nil {
			return true
		}
		switch cmd := l.Stmt.Cmd.(type) {
		case *syntax.BashPPSwitch:
			if cmd.TypeSwitch {
				labeled = append(labeled, l.Label.Value+":switch")
			}
		case *syntax.Block:
			if sw, ok := cmd.Stmts[len(cmd.Stmts)-1].Cmd.(*syntax.BashPPSwitch); ok && sw.TypeSwitch {
				labeled = append(labeled, l.Label.Value+":block")
			}
		}
		return true
	})
	if got, want := strings.Join(labeled, " "), "outer:switch again:block"; got != want {
		t.Fatalf("labeled type switches: got %q, want %q", got, want)
	}

	// Negatives: the checker's verdicts on the init are unchanged (a
	// declared-and-unused init name, a non-interface guard operand), and a
	// type switch without an init is still the bare switch statement.
	load := func(src string) (*gosource.Program, error) {
		return gosource.Load([]gosource.Source{{Name: "negative.go", Data: []byte(src)}}, gosource.Options{RunMain: true})
	}
	if _, err := load("package main\n\nfunc main() {\n\tvar x any = 1\n\tswitch y := 2; x.(type) {\n\tcase int:\n\t}\n}\n"); err == nil || !strings.Contains(err.Error(), "declared and not used: y") {
		t.Fatalf("unused init verdict changed: %v", err)
	}
	if _, err := load("package main\n\nfunc main() {\n\tswitch x := 2; x.(type) {\n\tcase int:\n\t}\n}\n"); err == nil || !strings.Contains(err.Error(), "not an interface") {
		t.Fatalf("non-interface guard verdict changed: %v", err)
	}
	bare, err := load("package main\n\nfunc main() {\n\tvar x any = 1\n\tswitch x.(type) {\n\tcase int:\n\t}\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	syntax.Walk(bare.File, func(n syntax.Node) bool {
		if block, ok := n.(*syntax.Block); ok && len(block.Stmts) == 1 {
			if sw, ok := block.Stmts[0].Cmd.(*syntax.BashPPSwitch); ok && sw.TypeSwitch {
				t.Fatalf("a type switch without an init gained a scoping block")
			}
		}
		return true
	})
}
