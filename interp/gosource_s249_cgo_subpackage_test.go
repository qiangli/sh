//go:build full

package interp_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower"
	"mvdan.cc/sh/v3/syntax"
)

// Sprint: #249; Story: #715; Story-ID: 90f96d4f4dae
func TestGoSourceS249LocalCgoSubpackage(t *testing.T) {
	t.Skip("blocked: the flattened package map does not retain cgo preambles or package-scoped C bindings, and the native worker forces CGO_ENABLED=0")

	dir := t.TempDir()
	mainSource := []byte(`package main

import "example.com/cgosubpackage/bad"

func main() { bad.Bad() }
`)
	badSource := []byte(`package bad

/*
#include <stdlib.h>
*/
import "C"

func Bad() {
	type L struct {
		Outer []byte
		Nested struct{ Inner []byte }
	}
	zero := map[string]L{}["missing"]
	if len(zero.Outer) != 0 || len(zero.Nested.Inner) != 0 {
		panic("map miss did not produce a zero value")
	}
	p := C.malloc(100)
	if p == nil {
		panic("malloc failed")
	}
	C.free(p)
}
`)
	write := func(name string, data []byte) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", []byte("module example.com/cgosubpackage\n\ngo 1.27\n"))
	write("main.go", mainSource)
	write("bad/bad.go", badSource)

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	native := exec.CommandContext(ctx, "go", "run", ".")
	native.Dir = dir
	if output, err := native.CombinedOutput(); err != nil {
		t.Fatalf("native control: %v: %s", err, output)
	}

	program, err := gosource.Load([]gosource.Source{{Name: filepath.Join(dir, "main.go"), Data: mainSource}}, gosource.Options{
		RunMain:     true,
		FakeImportC: true,
		ImportPath:  "example.com/cgosubpackage",
		Packages: []gosource.PackageSpec{{
			Path:    "example.com/cgosubpackage/bad",
			Sources: []gosource.Source{{Name: filepath.Join(dir, "bad/bad.go"), Data: badSource}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("interpreted", func(t *testing.T) {
		var stderr bytes.Buffer
		runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(dir), interp.GoSourceModuleDir(dir), interp.StdIO(nil, nil, &stderr))
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.Run(ctx, program.File); err != nil {
			t.Fatalf("Runner: %v: %s", err, stderr.String())
		}
	})

	t.Run("compiled", func(t *testing.T) {
		result, err := lower.Compile(program.File, lower.Options{Package: program.Package, Dir: dir, Importer: program.Importer})
		if err != nil {
			t.Fatal(err)
		}
		compiledDir := filepath.Join(dir, "compiled")
		root, err := filepath.Abs("..")
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join("compiled", "go.mod"), []byte("module example.com/cgosubpackage/compiled\n\ngo 1.27\n\nrequire mvdan.cc/sh/v3 v3.0.0\nreplace mvdan.cc/sh/v3 => "+root+"\n"))
		write(filepath.Join("compiled", "main.go"), result.Source)
		compiled := exec.CommandContext(ctx, "go", "run", ".")
		compiled.Dir = compiledDir
		if output, err := compiled.CombinedOutput(); err != nil {
			t.Fatalf("compiled control: %v: %s\n--- generated ---\n%s", err, output, result.Source)
		}
	})
}
