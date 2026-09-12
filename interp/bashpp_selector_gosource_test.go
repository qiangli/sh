package interp_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestGoSourcePointerCallResult(t *testing.T) {
	path := filepath.Join("..", "gosource", "testdata", "sprint151", "pointers", "call_pointer.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command("go", "run", path).Output()
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
	if err := runner.Run(context.Background(), program.File); err != nil || stderr.Len() != 0 {
		t.Fatalf("interpreter: err=%v stderr=%q", err, stderr.String())
	}
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Fatalf("stdout differs from go run\ninterpreter: %q\ngo run:      %q", stdout.Bytes(), want)
	}
}
