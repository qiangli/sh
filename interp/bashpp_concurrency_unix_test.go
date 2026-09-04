// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build unix

package interp

import (
	"strings"
	"syscall"
	"testing"
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
