package interp_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const s249PackageTestMainIdentity = "example.com/lib.test"

type s249GoSourceOutcome struct {
	stdout string
	stderr string
	status int
}

func runS249PackageTestMain(t *testing.T, sources []gosource.Source, packages []gosource.PackageSpec) (s249GoSourceOutcome, error) {
	t.Helper()
	program, err := gosource.Load(sources, gosource.Options{
		RunMain: true, ImportPath: s249PackageTestMainIdentity, TestMain: true, Packages: packages,
	})
	if err != nil {
		return s249GoSourceOutcome{}, err
	}
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.Lang(syntax.LangBashPP),
		interp.StdIO(nil, &stdout, &stderr),
		interp.GoSourceIdentity(s249PackageTestMainIdentity, true),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	err = runner.Run(ctx, program.File)
	outcome := s249GoSourceOutcome{stdout: stdout.String(), stderr: stderr.String()}
	var status interp.ExitStatus
	if errors.As(err, &status) {
		outcome.status = int(status)
		err = nil
	}
	return outcome, err
}

func s249Source(name, data string) gosource.Source {
	return gosource.Source{Name: name, Data: []byte(data)}
}

// cmd/go's package test harness keeps the descriptor slices in _testmain.go
// but points their function fields at a separately compiled/imported test
// package. The authenticated test-main fact must let testing.MainStart retain
// those exact descriptor slices instead of receiving copied descriptors, while
// the test bodies still execute under the interpreter.
func TestGoSourceS249PackageTestMainTransfersImportedDescriptors(t *testing.T) {
	lib := gosource.PackageSpec{Path: "example.com/lib", Sources: []gosource.Source{s249Source("lib.go", `package lib

const Name = "lib"
`)}}
	xtest := gosource.PackageSpec{Path: "example.com/lib_test", Sources: []gosource.Source{s249Source("lib_test.go", `package lib_test

import (
	"fmt"
	"testing"
)

func TestAlpha(t *testing.T) {
	fmt.Println("alpha from imported test")
}
`)}}
	driver := s249Source("_testmain.go", `package main

import (
	"os"
	"testing"
	"testing/internal/testdeps"

	_ "example.com/lib"
	_xtest "example.com/lib_test"
)

var tests = []testing.InternalTest{
	{"TestAlpha", _xtest.TestAlpha},
}

var benchmarks = []testing.InternalBenchmark{}
var fuzzTargets = []testing.InternalFuzzTarget{}
var examples = []testing.InternalExample{}

func init() {
	testdeps.ModulePath = "example.com/lib"
	testdeps.ImportPath = "example.com/lib"
}

func main() {
	m := testing.MainStart(testdeps.TestDeps{}, tests, benchmarks, fuzzTargets, examples)
	os.Exit(m.Run())
}
`)

	got, err := runS249PackageTestMain(t, []gosource.Source{driver}, []gosource.PackageSpec{lib, xtest})
	if err != nil {
		t.Fatalf("Runner: %v; stderr: %s", err, got.stderr)
	}
	want := s249GoSourceOutcome{stdout: "alpha from imported test\nPASS\n"}
	if got != want {
		t.Fatalf("Runner %+v; want %+v", got, want)
	}
}

func TestGoSourceS249PackageTestMainRequiresAuthenticatedFact(t *testing.T) {
	driver := s249Source("_testmain.go", `package main

import "testing/internal/testdeps"

func main() { _ = testdeps.TestDeps{} }
`)
	_, err := gosource.Load([]gosource.Source{driver}, gosource.Options{
		RunMain: true, ImportPath: "example.com/lib.test",
	})
	if err == nil || !strings.Contains(err.Error(), "testing/internal/testdeps") {
		t.Fatalf("Load err = %v, want unauthenticated internal-package refusal", err)
	}
}
