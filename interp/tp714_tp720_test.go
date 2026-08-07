// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// TestTP714TrapAfterIgnoreCatchesSignal verifies TP714: after ignoring a
// signal with `trap ” SIG` and then trapping it with `trap 'action' SIG`,
// the trap fires. signal.Notify alone cannot re-enable delivery after
// signal.Ignore cleared the Go runtime's handling bit; the fix uses
// signal.Reset before Notify during the ignore→trap transition.
func TestTP714TrapAfterIgnoreCatchesSignal(t *testing.T) {
	defer func() {
		signal.Reset(syscall.SIGUSR1)
	}()

	var buf bytes.Buffer
	r, err := New(StdIO(strings.NewReader(""), &buf, &buf))
	if err != nil {
		t.Fatal(err)
	}
	src := "trap '' USR1\n" +
		"trap 'echo caught' USR1\n" +
		"kill -s USR1 $$\n" +
		"echo after\n"
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		t.Logf("run error (may be expected): %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "caught") {
		t.Errorf("trap did not fire after ignore→trap transition\noutput: %q", out)
	}
	if !strings.Contains(out, "after") {
		t.Errorf("script did not continue after trap\noutput: %q", out)
	}
}

// TestTP714TrapFiresDuringBlockedRead verifies TP714's read interruption:
// a trapped signal fires while the shell is blocked in a read builtin,
// then the read resumes.
func TestTP714TrapFiresDuringBlockedRead(t *testing.T) {
	defer func() {
		signal.Reset(syscall.SIGUSR1)
	}()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	src := "trap 'echo trap_fired' USR1\n" +
		"echo ready_for_read\n" +
		"read x\n" +
		"echo got:$x\n"
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	r, err := New(StdIO(pr, &buf, &buf))
	if err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() { errc <- r.Run(context.Background(), file) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "ready_for_read\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(buf.String(), "ready_for_read\n") {
		t.Fatalf("shell did not reach read; output: %q", buf.String())
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "trap_fired\n") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := pw.WriteString("data\n"); err != nil {
		t.Fatal(err)
	}
	pw.Close()

	select {
	case <-errc:
	case <-time.After(5 * time.Second):
		t.Fatal("run timed out")
	}

	result := buf.String()
	if !strings.Contains(result, "trap_fired") {
		t.Errorf("trap did not fire during read\noutput: %q", result)
	}
	if !strings.Contains(result, "got:data") {
		t.Errorf("read did not resume after trap\noutput: %q", result)
	}
}

// TestTP720StandaloneResetSkipsInheritedIgnore verifies TP720:
// WithSignalResetter must not clear a signal that is SIG_IGN at the OS
// level. The reset loop probes the OS disposition before calling
// restoreExecSignal.
func TestTP720StandaloneResetSkipsInheritedIgnore(t *testing.T) {
	setOSIgnore(syscall.SIGUSR1)
	defer func() {
		signal.Reset(syscall.SIGUSR1)
	}()

	if !osSignalIgnored(syscall.SIGUSR1) {
		t.Fatal("precondition: USR1 should be SIG_IGN")
	}

	_, err := New(WithSignalResetter(OSSignalResetter{}))
	if err != nil {
		t.Fatal(err)
	}

	if !osSignalIgnored(syscall.SIGUSR1) {
		t.Fatal("WithSignalResetter cleared an inherited SIG_IGN disposition")
	}
}
