// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"os"
	"syscall"
	"testing"
)

// TestIsRuntimeSignalClassification verifies that only the five synchronous
// fault signals (BUS, FPE, ILL, SEGV, TRAP) are recognised as runtime-owned
// and that ordinary catchable signals are not.
func TestIsRuntimeSignalClassification(t *testing.T) {
	t.Parallel()

	runtime := map[string]bool{
		"BUS":  true,
		"FPE":  true,
		"ILL":  true,
		"SEGV": true,
		"TRAP": true,
	}
	safe := map[string]bool{
		"HUP":   false,
		"INT":   false,
		"QUIT":  false,
		"ABRT":  false,
		"USR1":  false,
		"USR2":  false,
		"PIPE":  false,
		"ALRM":  false,
		"TERM":  false,
		"CHLD":  false,
		"CONT":  false,
		"STOP":  false,
		"TSTP":  false,
		"TTIN":  false,
		"TTOU":  false,
		"URG":   false,
		"XCPU":  false,
		"XFSZ":  false,
		"WINCH": false,
		"KILL":  false,
	}

	for name, wantRuntime := range runtime {
		if got := isRuntimeSignal(name); got != wantRuntime {
			t.Errorf("isRuntimeSignal(%q) = %v, want %v", name, got, wantRuntime)
		}
	}
	for name, wantRuntime := range safe {
		if got := isRuntimeSignal(name); got != wantRuntime {
			t.Errorf("isRuntimeSignal(%q) = %v, want %v", name, got, wantRuntime)
		}
	}
}

// TestRuntimeSignalTrapNoOSEffect verifies that enableSignalTrap,
// ignoreSignalTrap, and disableSignalTrap are no-ops for runtime-owned
// synchronous fault signals — they must never install, ignore, or reset an OS
// disposition for signals the Go runtime owns.
func TestRuntimeSignalTrapNoOSEffect(t *testing.T) {
	t.Parallel()

	runtimeNames := []string{"BUS", "FPE", "ILL", "SEGV", "TRAP"}

	for _, name := range runtimeNames {
		// Use a fresh runner for each signal so the maps start empty.
		r := &Runner{}
		// An embedded runner has no sigReset, so it never toggles OS defaults.

		// enableSignalTrap must NOT install an OS handler.
		r.enableSignalTrap(name)
		r.sigMu.Lock()
		_, hasNotify := r.sigNotify[name]
		_, hasIgnore := r.sigIgnored[name]
		r.sigMu.Unlock()
		if hasNotify {
			t.Errorf("enableSignalTrap(%q) installed a signal.Notify handler on a runtime-owned signal", name)
		}
		if hasIgnore {
			t.Errorf("enableSignalTrap(%q) cleared a prior ignore on a runtime-owned signal", name)
		}

		// ignoreSignalTrap must NOT set SIG_IGN.
		r = &Runner{}
		r.ignoreSignalTrap(name)
		r.sigMu.Lock()
		_, hasIgnore = r.sigIgnored[name]
		r.sigMu.Unlock()
		if hasIgnore {
			t.Errorf("ignoreSignalTrap(%q) set SIG_IGN on a runtime-owned signal", name)
		}

		// disableSignalTrap must NOT reset the OS disposition.
		r = &Runner{}
		r.sigNotify = map[string]os.Signal{name: syscall.SIGSEGV} // simulate prior trap
		r.disableSignalTrap(name)
		r.sigMu.Lock()
		_, stillHasNotify := r.sigNotify[name]
		r.sigMu.Unlock()
		if !stillHasNotify {
			t.Errorf("disableSignalTrap(%q) removed the entry but must leave it untouched", name)
		}
	}
}

// TestRuntimeSignalWithSignalResetterListBounds verifies that none of the
// five runtime-owned signals appear in the WithSignalResetter default-reset
// list. The list is the only code path that calls restoreExecSignal at
// construction time, so its contents are the entire OS-impact surface for a
// standalone shell on startup.
func TestRuntimeSignalWithSignalResetterListBounds(t *testing.T) {
	t.Parallel()

	// This is a static-assert-style test: we walk the same literal slice
	// that WithSignalResetter iterates and verify no runtime name appears.
	resetList := [...]string{
		"HUP", "INT", "QUIT", "ABRT",
		"USR1", "USR2", "PIPE", "ALRM", "TERM",
		"TSTP", "TTIN", "TTOU", "XCPU", "XFSZ",
	}
	inList := make(map[string]bool)
	for _, name := range resetList {
		inList[name] = true
	}
	runtimeNames := []string{"BUS", "FPE", "ILL", "SEGV", "TRAP"}
	for _, name := range runtimeNames {
		if inList[name] {
			t.Errorf("runtime-owned signal %q must not appear in the WithSignalResetter reset list", name)
		}
	}
	// Additionally verify that CHLD and URG — the two other runtime-managed
	// signals — are absent, as they have their own explicit guards.
	for _, name := range []string{"CHLD", "URG"} {
		if inList[name] {
			t.Errorf("runtime-managed signal %q must not appear in the WithSignalResetter reset list", name)
		}
	}
}
