//go:build full

package interp_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// Sprint: #249; Story: #715; Story-ID: 90f96d4f4dae
//
// A companion build is decided by the original package directory, and two of
// the things it decides are not the interpreted program's to arrange. The
// generated helper's language version must be its own: a go.mod carrying no go
// directive means go1.16 semantics for every file in that module, which the
// helper does not compile under. And the build must compile none of the
// package's Go files, not merely the one input the runner was handed: a second
// file redeclares the very symbols the helper exists to declare.
func TestGoSourceS249CompanionModuleWithoutGoDirective(t *testing.T) {
	if testing.Short() {
		t.Skip("builds Go assembly companions")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("no assembly companion for GOARCH=%s", runtime.GOARCH)
	}
	declSource := `package main

func AsmTriple(v int) int
`
	mainSource := `package main

import "fmt"

func scale(v int) int { return v * 3 }

func main() {
	fmt.Println(AsmTriple(14), scale(2))
}
`
	dir := writeGoSourceCompanionDir(t, map[string]string{
		// No go directive at all, exactly as an upstream run-in-directory
		// module writes it.
		"go.mod":  "module example.com/nogodirective\n",
		"decl.go": declSource,
		"main.go": mainSource,
		"triple_amd64.s": `#include "textflag.h"

TEXT ·AsmTriple(SB), NOSPLIT, $0-16
	MOVQ v+0(FP), AX
	IMULQ $3, AX
	MOVQ AX, ret+8(FP)
	RET
`,
		"triple_arm64.s": `#include "textflag.h"

TEXT ·AsmTriple(SB), NOSPLIT, $0-16
	MOVD v+0(FP), R0
	MOVD $3, R1
	MUL R1, R0, R0
	MOVD R0, ret+8(FP)
	RET
`,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	var wantOut, wantErr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &wantOut, &wantErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run: %v: %s", err, wantErr.String())
	}

	program, err := gosource.Load([]gosource.Source{
		{Name: filepath.Join(dir, "decl.go"), Data: []byte(declSource)},
		{Name: filepath.Join(dir, "main.go"), Data: []byte(mainSource)},
	}, gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var gotOut, gotErr bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(dir), interp.GoSourceModuleDir(dir), interp.StdIO(nil, &gotOut, &gotErr))
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx, program.File); err != nil {
		t.Fatalf("Runner: %v: %s", err, gotErr.String())
	}
	if gotOut.String() != wantOut.String() || gotErr.String() != "" {
		t.Fatalf("Runner stdout=%q stderr=%q; want stdout=%q", gotOut.String(), gotErr.String(), wantOut.String())
	}
	if strings.TrimSpace(wantOut.String()) != "42 6" {
		t.Fatalf("reference stdout=%q, want %q", wantOut.String(), "42 6\n")
	}
}

// Sprint: #249; Story: #715; Story-ID: 90f96d4f4dae
//
// Blocker reproducer, left skipped. Upstream's own fixedbugs/issue15609.dir
// declares its companion as `TEXT ·jump(SB),NOSPLIT,$8` -- a non-zero frame
// with no NO_LOCAL_POINTERS, so that frame carries no stackmap. Natively the
// function it calls is a two-instruction leaf and nothing ever needs one. The
// trampoline that keeps that function interpreted is callback protocol, which
// both grows the stack and is a point where the collector may scan it, and the
// runtime answers either with `fatal error: missing stackmap`. That is the
// trampoline's shape, not the companion build's: with the build fixed, the
// leaf's interpreted issue15609 reaches main.jump -> main.target -> callback
// and dies there, while issue74648, whose companion has no frame, passes.
func TestGoSourceS249CompanionFramedAssemblyCallsInterpreted(t *testing.T) {
	t.Skip("blocked: a framed assembly companion with no NO_LOCAL_POINTERS has no stackmap, and a callback trampoline needs one (bashPPCompanionTrampolineGo, interp/gosource_companion_symbols.go)")
}
