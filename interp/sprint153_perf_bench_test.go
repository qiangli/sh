package interp_test

// Sprint: #153; Story: S153.6; Story-ID: 11fffa6b51be
//
// Interpreter call-path benchmarks over the outside-corpus scaled controls
// under testdata/sprint153/deadline/<root>/sizeN/main.go. Each benchmark
// loads one control through gosource and runs it through the interpreter;
// the native oracle is not consulted here because the controls are already
// pinned by TestSprint153Evaluator. Select one with
//
//	go test -run '^$' -bench 'Sprint153Deadline/peano/size6' -benchmem -cpuprofile cpu.out ./interp
import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func BenchmarkSprint153Deadline(b *testing.B) {
	root := filepath.Join("testdata", "sprint153", "deadline")
	roots, err := os.ReadDir(root)
	if err != nil {
		b.Fatal(err)
	}
	for _, r := range roots {
		if !r.IsDir() {
			continue
		}
		sizes, err := os.ReadDir(filepath.Join(root, r.Name()))
		if err != nil {
			b.Fatal(err)
		}
		for _, size := range sizes {
			if !size.IsDir() {
				continue
			}
			path := filepath.Join(root, r.Name(), size.Name(), "main.go")
			source, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			b.Run(r.Name()+"/"+size.Name(), func(b *testing.B) {
				benchGoSource(b, path, string(source))
			})
		}
	}
}

func benchGoSource(b *testing.B, path, source string) {
	b.Helper()
	dir := b.TempDir()
	for b.Loop() {
		program, err := gosource.Parse(strings.NewReader(source), path, gosource.Options{RunMain: true})
		if err != nil {
			b.Fatalf("gosource.Parse: %v", err)
		}
		var stdout, stderr bytes.Buffer
		runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(dir),
			interp.StdIO(strings.NewReader(""), &stdout, &stderr), interp.Params("--"))
		if err != nil {
			b.Fatal(err)
		}
		err = runner.Run(context.Background(), program.File)
		var status interp.ExitStatus
		if err != nil && !errorsAsExit(err, &status) {
			b.Fatalf("Runner: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	}
}

func errorsAsExit(err error, status *interp.ExitStatus) bool {
	return errors.As(err, status)
}

// BenchmarkSprint153File runs one Go program named by SPRINT153_BENCH_FILE
// through the interpreter, for profiling a program that is not a checked-in
// control (a corpus root, or a scratch program at a larger size).
func BenchmarkSprint153File(b *testing.B) {
	path := os.Getenv("SPRINT153_BENCH_FILE")
	if path == "" {
		b.Skip("SPRINT153_BENCH_FILE not set")
	}
	source, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	benchGoSource(b, path, string(source))
}
