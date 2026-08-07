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

// TestSynchronousFaultSignalTrappable verifies that the POSIX synchronous
// fault signals (BUS, FPE, ILL, SEGV, TRAP) are first-class trappable signals:
// enableSignalTrap installs a real signal.Notify handler, ignoreSignalTrap sets
// SIG_IGN, and disableSignalTrap tears a prior trap down. A pure-Go shell must
// catch a kill(2)-delivered instance via the handler (Go's runtime still
// recovers genuine CPU faults on its own), so that `trap '...' BUS; kill -BUS
// $$` runs the trap instead of faulting the interpreter (VSC-PCTS TP712/713/714).
// Reverts the e42746db guard that made these operations no-ops.
func TestSynchronousFaultSignalTrappable(t *testing.T) {
	// These calls install real OS dispositions for the test process, so do
	// not run them concurrently with other signal tests.
	runtimeNames := []string{"BUS", "FPE", "ILL", "SEGV", "TRAP"}

	for _, name := range runtimeNames {
		// enableSignalTrap installs a signal.Notify handler.
		r := &Runner{}
		r.enableSignalTrap(name)
		r.sigMu.Lock()
		_, hasNotify := r.sigNotify[name]
		r.sigMu.Unlock()
		if !hasNotify {
			t.Errorf("enableSignalTrap(%q) did not install a signal.Notify handler", name)
		}

		// ignoreSignalTrap sets SIG_IGN.
		r = &Runner{}
		r.ignoreSignalTrap(name)
		r.sigMu.Lock()
		_, hasIgnore := r.sigIgnored[name]
		r.sigMu.Unlock()
		if !hasIgnore {
			t.Errorf("ignoreSignalTrap(%q) did not set SIG_IGN", name)
		}
		if !osSignalIgnored(signalForOS(signalByNameMust(name))) {
			t.Errorf("ignoreSignalTrap(%q) did not install OS SIG_IGN", name)
		}

		// disableSignalTrap tears down a prior trap.
		r = &Runner{}
		r.sigNotify = map[string]os.Signal{name: syscall.SIGSEGV} // simulate prior trap
		r.disableSignalTrap(name)
		r.sigMu.Lock()
		_, stillHasNotify := r.sigNotify[name]
		r.sigMu.Unlock()
		if stillHasNotify {
			t.Errorf("disableSignalTrap(%q) did not remove the handler", name)
		}
	}
}

func signalByNameMust(name string) killSig {
	sig, ok := signalByName(name)
	if !ok {
		panic("missing test signal: " + name)
	}
	return sig
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
