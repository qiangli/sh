package interp_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// TestGoSourceOrderedOutputInterleaving uses one writer for both descriptors:
// its byte sequence is the observable ordering between stdout and stderr.
func TestGoSourceOrderedOutputInterleaving(t *testing.T) {
	path := filepath.Join("testdata", "sprint153", "output", "interleaving", "main.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for run := 0; run != 2; run++ {
		want := runOrderedNative(t, path)
		got := runOrderedInterpreter(t, path, string(source))
		if got.status != want.status || got.combined != want.combined {
			t.Fatalf("native bridge output barrier violated: run %d interpreter=%+v native=%+v", run, got, want)
		}
		if !orderedOutputEqual(got, want) {
			t.Fatal("equal captures compared unequal")
		}
		corrupt := got
		corrupt.combined = "native-stderr-2\\n" + strings.Replace(corrupt.combined, "native-stderr-2\\n", "", 1)
		if orderedOutputEqual(corrupt, want) {
			t.Fatal("merged/corrupted output order compared equal")
		}
	}
	if got := runOrderedInterpreter(t, filepath.Join("testdata", "sprint153", "output", "interleaving", "supported.go"), `package main
import "fmt"
func main() { fmt.Println("native-stdout-only") }`); got != (orderedOutput{combined: "native-stdout-only\n"}) {
		t.Fatalf("supported control: %+v", got)
	}
}

func TestGoSourcePanicHonorsGoTracebackNone(t *testing.T) {
	got := runOrderedInterpreter(t, "panic.go", "package main\nfunc main(){panic(\"final-panic\")}\n")
	want := orderedOutput{combined: "panic: final-panic\n", status: 2}
	if got != want {
		t.Fatalf("interpreter=%+v want=%+v", got, want)
	}
}

type orderedOutput struct {
	combined string
	status   int
}

func orderedOutputEqual(got, want orderedOutput) bool { return got == want }

func runOrderedNative(t *testing.T, path string) orderedOutput {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "native")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, path)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build native: %v: %s", err, output)
	}
	var combined bytes.Buffer
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "GOTRACEBACK=none")
	cmd.Stdout, cmd.Stderr = &combined, &combined
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("native run: %v", err)
	}
	return orderedOutput{combined: combined.String(), status: exit.ExitCode()}
}

func runOrderedInterpreter(t *testing.T, path, source string) orderedOutput {
	t.Helper()
	p, err := gosource.Parse(strings.NewReader(source), path, gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var combined bytes.Buffer
	r, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Env(expand.ListEnviron("GOTRACEBACK=none")), interp.StdIO(nil, &combined, &combined))
	if err != nil {
		t.Fatal(err)
	}
	err = r.Run(context.Background(), p.File)
	var status interp.ExitStatus
	if err == nil {
		return orderedOutput{combined: combined.String()}
	}
	if !errors.As(err, &status) {
		t.Fatalf("interpreter run: %v; output=%q", err, combined.String())
	}
	return orderedOutput{combined: combined.String(), status: int(status)}
}
