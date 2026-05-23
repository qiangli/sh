// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build unix

package interp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	qt "github.com/go-quicktest/qt"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// runScript parses and runs src under a fresh Runner, returning stdout+stderr
// merged and any execution error.
func runScript(t *testing.T, src string) (string, error) {
	t.Helper()
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	qt.Assert(t, qt.IsNil(err))
	var buf bytes.Buffer
	r, err := interp.New(interp.StdIO(nil, &buf, &buf))
	qt.Assert(t, qt.IsNil(err))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runErr := r.Run(ctx, file)
	return buf.String(), runErr
}

func TestKillSendsRealSignal(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary on PATH:", err)
	}
	// Spawn a 30s sleep externally so the runner gets a known real PID to
	// signal. The runner's own "&" backgrounding can't be used because $!
	// returns a "g<N>" sentinel, not a real PID.
	cmd := exec.Command(sleepBin, "30")
	qt.Assert(t, qt.IsNil(cmd.Start()))
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	script := fmt.Sprintf("kill %d", cmd.Process.Pid)
	out, err := runScript(t, script)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("runScript output: %q", out))

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		qt.Assert(t, qt.IsNotNil(waitErr), qt.Commentf("sleep should have been signalled"))
		var ee *exec.ExitError
		qt.Assert(t, qt.ErrorAs(waitErr, &ee))
		ws, ok := ee.Sys().(syscall.WaitStatus)
		qt.Assert(t, qt.IsTrue(ok))
		qt.Assert(t, qt.IsTrue(ws.Signaled()))
		qt.Assert(t, qt.Equals(ws.Signal(), syscall.SIGTERM))
	case <-time.After(5 * time.Second):
		t.Fatal("sleep did not exit within 5s of receiving SIGTERM")
	}
}

func TestKillExistenceProbe(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary on PATH:", err)
	}
	cmd := exec.Command(sleepBin, "30")
	qt.Assert(t, qt.IsNil(cmd.Start()))
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// signal 0 = existence probe, must succeed for a live PID
	out, err := runScript(t, fmt.Sprintf("kill -0 %d && echo alive", cmd.Process.Pid))
	qt.Assert(t, qt.IsNil(err), qt.Commentf("out: %q", out))
	qt.Assert(t, qt.Equals(strings.TrimSpace(out), "alive"))

	// 99999999 should not exist on any reasonable system. The kernel
	// returns ESRCH which our builtin reports as a non-zero exit.
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		out, err = runScript(t, "kill -0 99999999 || echo dead")
		qt.Assert(t, qt.IsNil(err), qt.Commentf("out: %q", out))
		qt.Assert(t, qt.IsTrue(strings.Contains(out, "dead")))
	}
}

func TestKillCustomSignal(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary on PATH:", err)
	}
	cmd := exec.Command(sleepBin, "30")
	qt.Assert(t, qt.IsNil(cmd.Start()))
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	out, err := runScript(t, fmt.Sprintf("kill -KILL %d", cmd.Process.Pid))
	qt.Assert(t, qt.IsNil(err), qt.Commentf("out: %q", out))

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case waitErr := <-done:
		var ee *exec.ExitError
		qt.Assert(t, qt.ErrorAs(waitErr, &ee))
		ws := ee.Sys().(syscall.WaitStatus)
		qt.Assert(t, qt.IsTrue(ws.Signaled()))
		qt.Assert(t, qt.Equals(ws.Signal(), syscall.SIGKILL))
	case <-time.After(5 * time.Second):
		t.Fatal("sleep did not exit within 5s of receiving SIGKILL")
	}
}

// guard against the package not being linked if errors lib changes
var _ = errors.New
