// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build !unix

package interp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// killSignals on non-Unix is the small set of signals the Go runtime can
// actually deliver via os.Process.Signal — Interrupt and Kill. SIGTERM is
// included because scripts portably use it; it's delivered as os.Kill on
// Windows where there is no graceful equivalent.
var killSignals = []struct {
	Name string
	Sig  syscall.Signal
}{
	{"INT", syscall.SIGINT},
	{"KILL", syscall.SIGKILL},
	{"TERM", syscall.SIGTERM},
}

func signalByName(name string) (syscall.Signal, bool) {
	name = strings.ToUpper(name)
	name = strings.TrimPrefix(name, "SIG")
	for _, e := range killSignals {
		if e.Name == name {
			return e.Sig, true
		}
	}
	return 0, false
}

func signalByNumber(n int) (syscall.Signal, string, bool) {
	if n == 0 {
		return 0, "EXIT", true
	}
	for _, e := range killSignals {
		if int(e.Sig) == n {
			return e.Sig, e.Name, true
		}
	}
	return 0, "", false
}

func sortedSignalEntries() []struct {
	Name string
	Sig  syscall.Signal
} {
	return killSignals
}

// continueIfStopped is a no-op on non-Unix: there is no SIGCONT analog,
// and this runner cannot suspend jobs on this platform anyway.
func continueIfStopped(pid int) {}

// sendSignal on non-Unix uses os.Process.Signal which only supports
// Interrupt and Kill. SIGTERM is mapped to Kill (no graceful equivalent
// exists on Windows). Signal 0 does an existence probe via os.FindProcess.
func sendSignal(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	switch sig {
	case 0:
		// Best-effort existence probe; FindProcess on Windows almost
		// always succeeds even for non-existent PIDs, so this is weak.
		return nil
	case syscall.SIGINT:
		return proc.Signal(os.Interrupt)
	case syscall.SIGKILL, syscall.SIGTERM:
		return proc.Kill()
	default:
		return fmt.Errorf("signal %d not supported on this platform", int(sig))
	}
}

func parseSignalSpec(spec string) (syscall.Signal, bool) {
	if n, err := strconv.Atoi(spec); err == nil {
		sig, _, ok := signalByNumber(n)
		return sig, ok
	}
	return signalByName(spec)
}
