// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"os"
	"os/signal"
	"syscall"
)

// forwardExecReplacementSignals preserves the parent-to-child half of an
// execve process boundary while the interpreter must proxy an exec replacement
// to keep live background jobs. Signals sent by the parent address the shell's
// PID, which a real execve would now belong to the replacement.
//
// SIGKILL and SIGSTOP cannot be caught and therefore remain an unavoidable
// proxy limitation. Signals already ignored at the OS boundary are skipped so
// the replacement retains execve's inherited SIG_IGN semantics.
func forwardExecReplacementSignals(pid int) func() {
	ch := make(chan os.Signal, 16)
	var forwarded []os.Signal
	var dispositions []signalDisposition
	for _, name := range [...]string{
		"HUP", "INT", "QUIT", "ABRT", "USR1", "USR2", "PIPE", "ALRM", "TERM",
		"TSTP", "TTIN", "TTOU", "XCPU", "XFSZ",
	} {
		sig, ok := signalByName(name)
		if !ok {
			continue
		}
		osSig := signalForOS(sig)
		if osSignalIgnored(osSig) {
			continue
		}
		disposition, ok := saveSignalDisposition(osSig)
		if !ok {
			continue
		}
		forwarded = append(forwarded, osSig)
		dispositions = append(dispositions, disposition)
	}
	if len(forwarded) == 0 {
		return func() {}
	}
	// OSSignalResetter may have installed SIG_DFL through raw sigaction after
	// clearing Go's handling bit. Synchronize os/signal's bookkeeping before
	// Notify so Linux reliably reinstalls the runtime trampoline.
	for _, sig := range forwarded {
		signal.Reset(sig)
	}
	signal.Notify(ch, forwarded...)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-done:
				return
			case sig := <-ch:
				if unixSig, ok := sig.(syscall.Signal); ok {
					_ = syscall.Kill(pid, unixSig)
				}
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
		<-finished
		for i, sig := range forwarded {
			restoreSignalDisposition(sig, dispositions[i])
		}
	}
}
