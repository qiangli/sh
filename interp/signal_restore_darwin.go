// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build darwin

package interp

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
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

// darwinSigaction calls the Darwin sigaction system call directly. This keeps
// the shell cgo-free without depending on private Go runtime symbols, whose
// linkname availability is intentionally not part of Go's compatibility
// contract.
func darwinSigaction(sig syscall.Signal, new, old *signalDisposition) bool {
	_, _, errno := syscall.RawSyscall(syscall.SYS_SIGACTION,
		uintptr(sig), uintptr(unsafe.Pointer(new)), uintptr(unsafe.Pointer(old)))
	return errno == 0
}

func saveSignalDisposition(sig os.Signal) (signalDisposition, bool) {
	var disposition signalDisposition
	s, ok := sig.(syscall.Signal)
	if !ok {
		return disposition, false
	}
	return disposition, darwinSigaction(s, nil, &disposition)
}

func restoreSignalDisposition(sig os.Signal, disposition signalDisposition) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return
	}
	darwinSigaction(s, &disposition, nil)
}

func restoreExecSignal(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		signal.Reset(sig)
		return
	}
	// A zero handler is SIG_DFL. Use libc so Darwin supplies the private
	// trampoline required at the kernel boundary.
	if !darwinSigaction(s, &signalDisposition{}, nil) {
		signal.Reset(sig)
	}
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
	if !darwinSigaction(s, &signalDisposition{handler: 1}, nil) {
		signal.Ignore(sig)
	}
}

// osSignalIgnored reports the real Darwin disposition, including SIG_IGN
// inherited across exec before os/signal has updated its bookkeeping.
func osSignalIgnored(sig os.Signal) bool {
	disposition, ok := saveSignalDisposition(sig)
	return ok && disposition.handler == 1
}
