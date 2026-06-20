// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"bytes"
	"syscall"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// fabricateWaitStatus builds a syscall.WaitStatus reporting a termination by
// sig, with the core-dump bit optionally set. Both Linux and the BSDs (macOS)
// encode the terminating signal in the low 7 bits and the core flag in 0x80,
// so this is portable across the `unix` build tag.
func fabricateWaitStatus(sig syscall.Signal, core bool) waitStatus {
	v := int(sig)
	if core {
		v |= 0x80
	}
	return syscall.WaitStatus(v)
}

// TestForegroundSignalDeathNotify covers #25/#26: a foreground external
// command killed by a fatal signal in a non-interactive shell prints a
// bash-style status line in default mode and prints nothing in POSIX mode.
// It exercises notifyForegroundSignalDeath directly with a fabricated
// WaitStatus so the assertions never depend on a real core dump (which the
// test host's `ulimit -c` would otherwise gate).
func TestForegroundSignalDeathNotify(t *testing.T) {
	t.Parallel()

	// Sanity-check the fabricated statuses behave like real ones.
	if st := fabricateWaitStatus(syscall.SIGSEGV, true); !st.Signaled() || !st.CoreDump() {
		t.Fatalf("fabricated SIGSEGV+core: Signaled=%v CoreDump=%v, want both true",
			st.Signaled(), st.CoreDump())
	}
	if st := fabricateWaitStatus(syscall.SIGTERM, false); !st.Signaled() || st.CoreDump() {
		t.Fatalf("fabricated SIGTERM: Signaled=%v CoreDump=%v, want true/false",
			st.Signaled(), st.CoreDump())
	}

	const pid = 12345
	args := []string{"sh", "-c", "kill -s SIG $$"}
	const cmd = "sh -c kill -s SIG $$"

	tests := []struct {
		name        string
		sig         syscall.Signal
		core        bool
		posix       bool
		interactive bool
		want        string
	}{
		{
			name: "segv default core",
			sig:  syscall.SIGSEGV, core: true,
			want: "bash: line 0: 12345 Segmentation fault (core dumped) " + cmd + "\n",
		},
		{
			name: "abrt default core",
			sig:  syscall.SIGABRT, core: true,
			want: "bash: line 0: 12345 Aborted (core dumped) " + cmd + "\n",
		},
		{
			name: "fpe default core uses arithmetic exception",
			sig:  syscall.SIGFPE, core: true,
			want: "bash: line 0: 12345 Arithmetic exception (core dumped) " + cmd + "\n",
		},
		{
			name: "term default bare",
			sig:  syscall.SIGTERM, core: false,
			want: "Terminated " + cmd + "\n",
		},
		{
			name: "int suppressed",
			sig:  syscall.SIGINT, core: false,
			want: "",
		},
		{
			name: "pipe suppressed",
			sig:  syscall.SIGPIPE, core: false,
			want: "",
		},
		{
			name: "segv posix suppressed",
			sig:  syscall.SIGSEGV, core: true, posix: true,
			want: "",
		},
		{
			name: "term posix suppressed",
			sig:  syscall.SIGTERM, core: false, posix: true,
			want: "",
		},
		{
			name: "segv interactive suppressed",
			sig:  syscall.SIGSEGV, core: true, interactive: true,
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{}
			r.bashCompatErrors = true
			r.filename = "bash"
			r.opts[optPosix] = tc.posix
			r.interactiveShell = tc.interactive

			var buf bytes.Buffer
			r.notifyForegroundSignalDeath(&buf, syntax.Pos{}, pid,
				fabricateWaitStatus(tc.sig, tc.core), args)

			if got := buf.String(); got != tc.want {
				t.Fatalf("notifyForegroundSignalDeath output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}
