package interp_test

// Sprint: #153; Story: S153.4; Story-ID: e58cccba74f8
//
// Interpreted evaluator semantics. Every program under
// testdata/sprint153/<mechanism>/ is an unchanged Go program that is run
// natively and through the interpreter; stdout, stderr and the exit status
// must agree exactly. Each mechanism directory holds the reproducer that
// used to diverge next to a positive control the interpreter already
// handled, so a regression in either direction is visible.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSprint153Evaluator(t *testing.T) {
	root := filepath.Join("testdata", "sprint153")
	mechanisms, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, mechanism := range mechanisms {
		if !mechanism.IsDir() || mechanism.Name() == "evaluator" {
			continue
		}
		programs, err := filepath.Glob(filepath.Join(root, mechanism.Name(), "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(programs) == 0 {
			t.Fatalf("%s: no programs", mechanism.Name())
		}
		for _, program := range programs {
			source, err := os.ReadFile(program)
			if err != nil {
				t.Fatal(err)
			}
			name := mechanism.Name() + "/" + strings.TrimSuffix(filepath.Base(program), ".go")
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				differGoSource(t, string(source), nil, "")
			})
		}
	}
}
