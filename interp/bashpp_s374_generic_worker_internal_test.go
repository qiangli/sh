package interp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
)

// typeparam/cons.go asks the dependency worker to register instantiated local
// generic types. Those registrations must name the declarations the worker
// actually emits, rather than the source package's unqualified names.
func TestS374GeneratedWorkerBuildsLocalGenericTypeRegistrations(t *testing.T) {
	program, err := gosource.Parse(strings.NewReader(`package main
type List[A any] interface{}
type Cons[A any] struct { Head A; Tail List[A] }
type Nil[A any] struct{}
func main() { var _ List[int] = Cons[int]{}; _ = Nil[int]{} }
`), "cons.go", gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{bashPPGoSourceFile: program.File}
	descriptors, _ := r.bashPPBuildLocalTypeDescriptors()
	source, err := bashPPNativeSource(context.Background(), bashPPEvalRequest{
		LocalTypes:   descriptors,
		GenericTypes: r.bashPPBuildGenericBridgeTypes(),
	})
	if err != nil {
		t.Fatal(err)
	}
	mailboxImports, mailboxSource := bashPPMailboxWorkerSource(false)
	source = strings.Replace(source, "//CALLBACKMAILBOXIMPORTS", mailboxImports, 1)
	source = strings.Replace(source, "//CALLBACKMAILBOX", mailboxSource, 1)
	source = strings.Replace(source, "//CONNECTION", `const bridgeNetwork = "tcp"
const bridgeAddress = "127.0.0.1:1"
const bridgeAuth = "0123456789abcdef0123456789abcdef"
const callbackMailboxPath = ""`, 1)
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if err := bashPPBuildWorkerImportcfg(context.Background(), goBin, t.TempDir(), os.Environ(), "", dir, path, filepath.Join(dir, "worker"), nil, ""); err != nil {
		t.Fatalf("build generated dependency worker: %v", err)
	}
}
