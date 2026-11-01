package lower_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/lower"
)

// TestGoSourcePointerInitializer drives a returned pointer through lowering
// and the installed supported compiler. The direct address initializer is the supported
// control. Exact output and status plus the deadline reject wrong values,
// stray output, wrong status, and hangs.
func TestGoSourcePointerInitializer(t *testing.T) {
	name := filepath.Join("testdata", "sprint165", "lower-1", "pointer-initializer", "main.go")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	program, err := gosource.Parse(bytes.NewReader(data), name, gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := lower.Compile(program.File, lower.Options{Origin: name, Package: program.Package})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	generated := filepath.Join(dir, "main.go")
	if err := os.WriteFile(generated, result.Source, 0o600); err != nil {
		t.Fatal(err)
	}
	// Compile separately: compiler diagnostics are not program output, and a
	// toolchain download must not consume the program's execution deadline.
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer buildCancel()
	binary := filepath.Join(dir, "program.exe")
	build := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, generated)
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
	diagnostics, err := build.CombinedOutput()
	if buildCtx.Err() != nil {
		t.Fatalf("generated program compilation exceeded its deadline: %v\n%s", buildCtx.Err(), diagnostics)
	}
	if err != nil {
		t.Fatalf("generated program compilation failed: %v\n%s\n--- generated\n%s", err, diagnostics, result.Source)
	}
	if len(diagnostics) > 0 {
		t.Logf("compiler diagnostics:\n%s", diagnostics)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("generated program exceeded its deadline")
	}
	if err != nil {
		t.Fatalf("generated program failed: %v\n%s\n--- generated\n%s", err, output, result.Source)
	}
	if string(output) != "7 9\n" {
		t.Fatalf("generated output = %q, want exactly %q\n--- generated\n%s", output, "7 9\n", result.Source)
	}
}
