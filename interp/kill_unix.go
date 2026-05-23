// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// killSignals is the bash-compatible signal table for the `kill` builtin.
// Order is the canonical numerical-ish listing bash uses for `kill -l`.
var killSignals = []struct {
	Name string
	Sig  syscall.Signal
}{
	{"HUP", unix.SIGHUP},
	{"INT", unix.SIGINT},
	{"QUIT", unix.SIGQUIT},
	{"ILL", unix.SIGILL},
	{"TRAP", unix.SIGTRAP},
	{"ABRT", unix.SIGABRT},
	{"BUS", unix.SIGBUS},
	{"FPE", unix.SIGFPE},
	{"KILL", unix.SIGKILL},
	{"USR1", unix.SIGUSR1},
	{"SEGV", unix.SIGSEGV},
	{"USR2", unix.SIGUSR2},
	{"PIPE", unix.SIGPIPE},
	{"ALRM", unix.SIGALRM},
	{"TERM", unix.SIGTERM},
	{"CHLD", unix.SIGCHLD},
	{"CONT", unix.SIGCONT},
	{"STOP", unix.SIGSTOP},
	{"TSTP", unix.SIGTSTP},
	{"TTIN", unix.SIGTTIN},
	{"TTOU", unix.SIGTTOU},
	{"URG", unix.SIGURG},
	{"XCPU", unix.SIGXCPU},
	{"XFSZ", unix.SIGXFSZ},
	{"VTALRM", unix.SIGVTALRM},
	{"PROF", unix.SIGPROF},
	{"WINCH", unix.SIGWINCH},
	{"IO", unix.SIGIO},
	{"SYS", unix.SIGSYS},
}

// signalByName resolves a bash-style signal name to its numeric value.
// Case-insensitive; accepts both "TERM" and "SIGTERM".
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

// signalByNumber resolves a numeric signal to a known entry. Signal 0 is the
// POSIX "no-op probe" — returned as-is so `kill -0 PID` works for existence
// checks even though 0 is not in the table.
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

// sortedSignalEntries returns the entries in numerical order for `kill -l`.
func sortedSignalEntries() []struct {
	Name string
	Sig  syscall.Signal
} {
	// killSignals is already in canonical bash listing order.
	return killSignals
}

// sendSignal delivers sig to pid. pid may be negative (process group target).
// Signal 0 performs error checking only — useful for existence probes.
func sendSignal(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}

// parseSignalSpec parses the part after the leading `-` in `kill -SPEC pid…`.
// SPEC is either a number or a name (with or without SIG prefix). Returns the
// resolved signal, or false if the spec is not recognized.
func parseSignalSpec(spec string) (syscall.Signal, bool) {
	if n, err := strconv.Atoi(spec); err == nil {
		sig, _, ok := signalByNumber(n)
		return sig, ok
	}
	return signalByName(spec)
}
