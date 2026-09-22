// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build windows

package interp

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// Sprint 245, story S245.8: the ntdll suspend/resume primitive, exercised
// against a real child process. Which signal picks which action is proven on
// any host in suspend_routing_test.go.

// suspendHelperEnv makes a re-executed copy of this test binary play the
// child: a process that keeps making observable progress until it is stopped.
const suspendHelperEnv = "GOSH_SUSPEND_TICKER"

func init() {
	if os.Getenv(suspendHelperEnv) == "" {
		return
	}
	// os.Stdout is unbuffered, so every tick is immediately visible to the
	// parent — which is what lets it tell "suspended" from "slow".
	for {
		if _, err := fmt.Fprintln(os.Stdout, "tick"); err != nil {
			os.Exit(0)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// startTicker re-executes this test binary as the ticking child and returns
// it together with a counter of the bytes it has produced so far. The parent
// drains the pipe continuously: a child that filled a pipe nobody reads would
// block on write and look suspended without being suspended.
func startTicker(t *testing.T) (*exec.Cmd, *atomic.Int64) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), suspendHelperEnv+"=1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	var seen atomic.Int64
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the ticker: %v", err)
	}
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := out.Read(buf)
			seen.Add(int64(n))
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = resumeProcess(cmd.Process.Pid) // never leave a stopped process behind
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		io.Copy(io.Discard, out)
	})
	return cmd, &seen
}

// waitForProgress fails unless the child produces more output than mark
// within a generous window.
func waitForProgress(t *testing.T, seen *atomic.Int64, mark int64, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if seen.Load() > mark {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: the child made no progress past %d bytes", what, mark)
}

func TestSuspendResumeProcess(t *testing.T) {
	cmd, seen := startTicker(t)
	pid := cmd.Process.Pid
	waitForProgress(t, seen, 0, "before the suspend")

	if err := suspendProcess(pid); err != nil {
		t.Fatalf("suspendProcess(%d): %v", pid, err)
	}
	// Let anything already in flight land, then hold still: a suspended
	// process must produce nothing at all over a window several hundred
	// ticks long.
	time.Sleep(200 * time.Millisecond)
	stopped := seen.Load()
	time.Sleep(500 * time.Millisecond)
	if grew := seen.Load(); grew != stopped {
		t.Fatalf("a suspended process kept running: %d bytes became %d", stopped, grew)
	}
	// A stopped process is still there, so `kill -0` succeeds on it.
	if err := probeProcess(pid); err != nil {
		t.Fatalf("probing a suspended process: %v", err)
	}

	if err := resumeProcess(pid); err != nil {
		t.Fatalf("resumeProcess(%d): %v", pid, err)
	}
	waitForProgress(t, seen, stopped, "after the resume")
}

// Resuming a process that was never suspended is SIGCONT to a running
// process: a successful no-op that leaves it running.
func TestResumeProcessNotSuspended(t *testing.T) {
	cmd, seen := startTicker(t)
	waitForProgress(t, seen, 0, "before the resume")
	if err := resumeProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("resumeProcess on a running process: %v", err)
	}
	waitForProgress(t, seen, seen.Load(), "after the no-op resume")
}

// A stopped process must still be killable, and must report the signal: on
// Unix SIGKILL is delivered to a stopped process without resuming it, and
// `kill -9` on a stopped job is how jobs.tests reaps its sleeps.
func TestKillSuspendedProcess(t *testing.T) {
	cmd, seen := startTicker(t)
	pid := cmd.Process.Pid
	waitForProgress(t, seen, 0, "before the suspend")
	if err := suspendProcess(pid); err != nil {
		t.Fatalf("suspendProcess(%d): %v", pid, err)
	}
	sig, ok := signalByName("KILL")
	if !ok {
		t.Fatal("no KILL in the Windows signal table")
	}
	if err := sendSignal(pid, sig); err != nil {
		t.Fatalf("kill -KILL on a suspended process: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a killed process exited successfully")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a suspended process did not die of kill -KILL")
	}
	status, ok := decodeWindowsWaitStatus(0, uint32(cmd.ProcessState.ExitCode()))
	if !ok || status.Signal() != sig.Num {
		t.Fatalf("exit code %#x decoded to %+v (ok=%v), want signal %d",
			uint32(cmd.ProcessState.ExitCode()), status, ok, sig.Num)
	}
}

// suspendProcess and resumeProcess report ESRCH for a process that is gone,
// as kill(2) does.
func TestSuspendProcessGone(t *testing.T) {
	cmd, _ := startTicker(t)
	pid := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("killing the ticker: %v", err)
	}
	_ = cmd.Wait()
	if err := suspendProcess(pid); err == nil {
		t.Error("suspending a dead process succeeded")
	}
	if err := resumeProcess(pid); err == nil {
		t.Error("resuming a dead process succeeded")
	}
}
