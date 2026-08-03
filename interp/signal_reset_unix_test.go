// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build unix

package interp_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTrapResetRestoresOSDefault is the regression test for VSC-PCTS
// TP712's POSIX trap default-reset semantics. A standalone shell that runs
//
//	trap '' USR2   # ignore
//	trap - USR2    # reset to default
//	read x         # block
//
// must again be terminated by an externally-delivered SIGUSR2 under the
// signal's default disposition. Before the WithSignalResetter seam the
// `trap ”` SIG_IGN survived the reset (Go's signal.Reset cannot undo
// signal.Ignore), so SIGUSR2 was silently swallowed and the shell hung.
//
// The scenario is driven in a re-exec'd child that opts into
// interp.OSSignalResetter, so the real OS default kill is observed by this
// test process without taking down the test binary itself.
func TestTrapResetRestoresOSDefault(t *testing.T) {
	t.Parallel()

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "GOSH_CMD=sig_reset_default")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Hold the write end of the child's stdin open so its `read x` blocks
	// (rather than seeing EOF and exiting cleanly before we can signal it).
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinW.Close()
	cmd.Stdin = stdinR

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stdinR.Close() // the child holds its own copy now

	// Wait for the child to announce it has reset the trap and is about to
	// block, so the signal lands under the restored default disposition.
	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) == "ready" {
				close(ready)
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("child never reported ready")
	}

	if err := cmd.Process.Signal(syscall.SIGUSR2); err != nil {
		t.Fatalf("signaling child: %v", err)
	}

	// The child must be terminated by SIGUSR2, not exit normally and not
	// hang. cmd.Wait runs in a goroutine so a regression shows up as a
	// timeout rather than a stuck test.
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("child did not exit with an error; got %v (a swallowed signal)", err)
		}
		ws, ok := ee.Sys().(syscall.WaitStatus)
		if !ok {
			t.Fatalf("unexpected wait status type %T", ee.Sys())
		}
		if !ws.Signaled() || ws.Signal() != syscall.SIGUSR2 {
			t.Fatalf("child not terminated by SIGUSR2: signaled=%v signal=%v code=%d",
				ws.Signaled(), ws.Signal(), ws.ExitStatus())
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("child hung after SIGUSR2; trap reset did not restore the OS default")
	}
}
