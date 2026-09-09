package interp_test

import (
	"bytes"
	"context"
	"os/exec"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// The optional source frontend may consume the runtime, but the runtime must
// not make classic shell consumers link the frontend through a reverse edge.
func TestGoSourceRuntimeDoesNotDependOnFrontend(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", "mvdan.cc/sh/v3/interp").CombinedOutput()
	if err != nil {
		t.Fatalf("dependency graph: %v: %s", err, output)
	}
	for _, dep := range bytes.Fields(output) {
		if string(dep) == "mvdan.cc/sh/v3/gosource" {
			t.Fatal("runtime links optional Go-source frontend")
		}
	}
}

func TestGoSourceTestingRejectsNilProgramView(t *testing.T) {
	r, err := interp.New(interp.Lang(syntax.LangBashPP))
	if err != nil {
		t.Fatal(err)
	}
	var program *gosource.Program
	for _, view := range []interp.GoSourceTestProgram{nil, program} {
		if _, err := r.LoadGoSourceTests(context.Background(), view); err == nil {
			t.Fatal("nil program accepted")
		}
	}
}
