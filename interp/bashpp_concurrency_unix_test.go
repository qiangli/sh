// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build unix

package interp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestBashPPGoArmsBeforeBlockingFIFORedirect(t *testing.T) {
	path := t.TempDir() + "/handshake.fifo"
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runBashPPConcurrency(t, strings.ReplaceAll(`
func reader(ack) { /bin/cat < FIFO; ack <- ok; }
func main() {
 ack := make(chan string)
 go reader(ack)
 echo ready > FIFO
 <-ack
}
main()
`, "FIFO", path))
	if err != nil || out != "ready\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestBashPPTaskSourceFIFORefusedWithoutHang(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocked.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var output strings.Builder
	var runErr error
	r, err := New(Lang(syntax.LangBashPP), StdIO(nil, &output, &output))
	if err != nil {
		t.Fatal(err)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(`
func load() { source `+path+`; }
func main() { go load(); }
main()
`), "fifo-source.bpp")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		runErr = r.Run(context.Background(), f)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task source FIFO blocked File join")
	}
	if runErr == nil || !strings.Contains(output.String(), "FIFO input is unavailable") {
		t.Fatalf("out=%q err=%v", output.String(), runErr)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestBashPPTaskRelativeOpenUsesRetainedDirectory(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "original")
	moved := filepath.Join(root, "moved")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(original, "payload.bpp"), []byte("echo sourced\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	r, err := New(Lang(syntax.LangBashPP), StdIO(nil, &output, &output))
	if err != nil {
		t.Fatal(err)
	}
	r.Dir = original
	src := `
func worker(start, ack) {
 <-start
 source payload.bpp
 echo redirected > result.txt
 ack <- ok
}
func main() {
 start := make(chan string)
 ack := make(chan string)
 go worker(start, ack)
 /bin/mv "` + original + `" "` + moved + `"
 start <- go
 <-ack
}
main()
`
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "retained-cwd.bpp")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), f); err != nil {
		t.Fatalf("out=%q err=%v", output.String(), err)
	}
	if output.String() != "sourced\n" {
		t.Fatalf("relative source missed retained directory: %q", output.String())
	}
	data, err := os.ReadFile(filepath.Join(moved, "result.txt"))
	if err != nil || string(data) != "redirected\n" {
		t.Fatalf("relative redirection missed retained directory: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("original cwd pathname unexpectedly remains: %v", err)
	}
}
