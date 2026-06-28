// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/syntax"
)

type killSig = syscall.Signal

func sigIsZero(s killSig) bool { return s == 0 }
func sigNum(s killSig) int     { return int(s) }

const defaultTermSignal killSig = unix.SIGTERM

// killSignals is the bash-compatible signal table for the `kill` builtin, in the
// canonical numerical listing order bash uses for `kill -l`. It is defined
// per-OS (kill_signals_linux.go / kill_signals_other.go) because the signal set
// differs by platform — Linux carries SIGSTKFLT/SIGPWR and the realtime signals
// SIGRTMIN..SIGRTMAX, which the BSDs/Darwin do not.

// signalByName resolves a bash-style signal name to its numeric value.
// Case-insensitive; accepts both "TERM" and "SIGTERM".
func signalByName(name string) (killSig, bool) {
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
func signalByNumber(n int) (killSig, string, bool) {
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
	Sig  killSig
} {
	// killSignals is already in canonical bash listing order.
	return killSignals
}

func signalNumber(sig killSig) (int, bool) {
	return int(sig), true
}

func signalName(sig killSig) (string, bool) {
	_, name, ok := signalByNumber(int(sig))
	return name, ok
}

// signalDescriptions maps a canonical signal name (no "SIG" prefix, as used
// by killSignals) to bash's human-readable description (j_strsignal /
// siglist.c), e.g. "Segmentation fault". Note that bash 5.3 reports SIGFPE as
// "Arithmetic exception" (not "Floating point exception"), which is what we
// reproduce here.
var signalDescriptions = map[string]string{
	"HUP":    "Hangup",
	"INT":    "Interrupt",
	"QUIT":   "Quit",
	"ILL":    "Illegal instruction",
	"TRAP":   "Trace/breakpoint trap",
	"ABRT":   "Aborted",
	"BUS":    "Bus error",
	"FPE":    "Arithmetic exception",
	"KILL":   "Killed",
	"USR1":   "User defined signal 1",
	"SEGV":   "Segmentation fault",
	"USR2":   "User defined signal 2",
	"PIPE":   "Broken pipe",
	"ALRM":   "Alarm clock",
	"TERM":   "Terminated",
	"CHLD":   "Child exited",
	"CONT":   "Continued",
	"STOP":   "Stopped (signal)",
	"TSTP":   "Stopped",
	"TTIN":   "Stopped (tty input)",
	"TTOU":   "Stopped (tty output)",
	"URG":    "Urgent I/O condition",
	"XCPU":   "CPU time limit exceeded",
	"XFSZ":   "File size limit exceeded",
	"VTALRM": "Virtual timer expired",
	"PROF":   "Profiling timer expired",
	"WINCH":  "Window changed",
	"IO":     "I/O possible",
	"SYS":    "Bad system call",
}

// signalDescription returns bash's human-readable description for sig, e.g.
// SIGSEGV -> "Segmentation fault". ok is false for signals with no known
// description (in which case no death notification is printed).
func signalDescription(sig killSig) (desc string, ok bool) {
	name, ok := signalName(sig)
	if !ok {
		return "", false
	}
	desc, ok = signalDescriptions[name]
	return desc, ok
}

// notifyForegroundSignalDeath prints bash's status line for a FOREGROUND
// external command that was killed by a fatal signal (#25/#26), mirroring
// jobs.c notify_of_job_status for a non-interactive shell:
//
//   - Only non-interactive shells notify; an interactive shell's job-control
//     reporting is handled elsewhere, so this is a no-op when interactiveShell.
//   - POSIX mode (`set -o posix`) stays completely silent, deferring the
//     report to `wait`/`jobs`.
//   - SIGINT and SIGPIPE are suppressed (bash does not announce these).
//   - A signal that dumped core prints the prefixed form
//     "<prog>: line N: <pid> <description> (core dumped) <cmd>"; any other
//     terminating signal (e.g. SIGTERM) prints the bare form
//     "<description> <cmd>". This core-dump gating reproduces every measured
//     bash 5.3 case (SEGV/ABRT/FPE/ILL/BUS prefixed+core, TERM bare).
//
// The exit code (128+signal) is set by the caller and unaffected here.
func (r *Runner) notifyForegroundSignalDeath(w io.Writer, pos syntax.Pos, pid int, status waitStatus, args []string) {
	if r == nil || r.opts[optPosix] || r.interactiveShell {
		return
	}
	sig := status.Signal()
	if sig == unix.SIGINT || sig == unix.SIGPIPE {
		return
	}
	desc, ok := signalDescription(sig)
	if !ok {
		return
	}
	cmd := strings.Join(args, " ")
	if status.CoreDump() {
		fmt.Fprintf(w, "%s%d %s (core dumped) %s\n", r.bashErrPrefix(pos), pid, desc, cmd)
		return
	}
	fmt.Fprintf(w, "%s %s\n", desc, cmd)
}

func signalForOS(sig killSig) os.Signal {
	return sig
}

// sendSignal delivers sig to pid. pid may be negative (process group target).
// Signal 0 performs error checking only — useful for existence probes.
func sendSignal(pid int, sig killSig) error {
	return syscall.Kill(pid, sig)
}

// continueIfStopped sends SIGCONT to pid, best-effort. Used by `fg` to
// resume a stopped real-PID bg process before waiting on it. Errors are
// swallowed because the process may already be running or gone.
func continueIfStopped(pid int) {
	_ = syscall.Kill(pid, syscall.SIGCONT)
}

func jobSignalPid(bg *bgProc) int {
	pid := int(bg.pid.Load())
	if bg.jobControl && pid > 0 {
		return -pid
	}
	return pid
}

func signalStopsJob(sig killSig) bool {
	return sig == unix.SIGSTOP || sig == unix.SIGTSTP || sig == unix.SIGTTIN || sig == unix.SIGTTOU
}

func signalContinuesJob(sig killSig) bool {
	return sig == unix.SIGCONT
}

// parseSignalSpec parses the part after the leading `-` in `kill -SPEC pid…`.
// SPEC is either a number or a name (with or without SIG prefix). Returns the
// resolved signal, or false if the spec is not recognized.
func parseSignalSpec(spec string) (killSig, bool) {
	if n, err := strconv.Atoi(spec); err == nil {
		sig, _, ok := signalByNumber(n)
		return sig, ok
	}
	return signalByName(spec)
}
