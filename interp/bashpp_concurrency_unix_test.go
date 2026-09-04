// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build unix

package interp

import (
	"context"
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
