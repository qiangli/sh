// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build darwin

package interp

import (
	"os"
	"os/signal"
	"syscall"
	_ "unsafe" // required by go:linkname
)

// signalDisposition matches Darwin's public struct sigaction, which is the
// shape accepted by libc sigaction: handler, 32-bit mask, and 32-bit flags.
// The kernel's private struct __sigaction additionally carries a trampoline;
// calling SYS_SIGACTION directly would have to manufacture that private field.
type signalDisposition struct {
	handler uintptr
	mask    uint32
	flags   int32
}

// runtimeSigaction uses the Go runtime's existing cgo-free libc trampoline.
// Darwin's exported syscall.Syscall family cannot call sigaction by syscall
// number (its first argument is a libc function pointer), while the runtime
// already owns the exact per-architecture bridge and public ABI conversion.
// Keep this declaration byte-for-byte compatible with runtime.usigactiont and
// runtime.sigaction.
//
//go:linkname runtimeSigaction runtime.sigaction
func runtimeSigaction(sig uint32, new, old *signalDisposition)

func saveSignalDisposition(sig os.Signal) (signalDisposition, bool) {
	var disposition signalDisposition
	s, ok := sig.(syscall.Signal)
	if !ok {
		return disposition, false
	}
	runtimeSigaction(uint32(s), nil, &disposition)
	return disposition, true
}

func restoreSignalDisposition(sig os.Signal, disposition signalDisposition) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return
	}
	runtimeSigaction(uint32(s), &disposition, nil)
}

func restoreExecSignal(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		signal.Reset(sig)
		return
	}
	// A zero handler is SIG_DFL. Use libc so Darwin supplies the private
	// trampoline required at the kernel boundary.
	runtimeSigaction(uint32(s), &signalDisposition{}, nil)
}

// setOSIgnore installs SIG_IGN for sig at the OS level without changing the
// os/signal package's bookkeeping. This is required for ignored-on-entry
// synchronous fault signals: a kill-delivered fault must be discarded while
// a genuine hardware fault remains owned by the Go runtime.
func setOSIgnore(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		signal.Ignore(sig)
		return
	}
	runtimeSigaction(uint32(s), &signalDisposition{handler: 1}, nil)
}

// osSignalIgnored reports the real Darwin disposition, including SIG_IGN
// inherited across exec before os/signal has updated its bookkeeping.
func osSignalIgnored(sig os.Signal) bool {
	disposition, ok := saveSignalDisposition(sig)
	return ok && disposition.handler == 1
}
