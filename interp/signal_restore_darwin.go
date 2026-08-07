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

type signalDisposition [16]byte

func saveSignalDisposition(sig os.Signal) (signalDisposition, bool) {
	var disposition signalDisposition
	s, ok := sig.(syscall.Signal)
	if !ok {
		return disposition, false
	}
	_, _, errno := syscall.RawSyscall(syscall.SYS_SIGACTION,
		uintptr(s), 0, uintptr(unsafe.Pointer(&disposition[0])))
	return disposition, errno == 0
}

func restoreSignalDisposition(sig os.Signal, disposition signalDisposition) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return
	}
	_, _, _ = syscall.RawSyscall(syscall.SYS_SIGACTION,
		uintptr(s), uintptr(unsafe.Pointer(&disposition[0])), 0)
}

func restoreExecSignal(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		signal.Reset(sig)
		return
	}
	var act [16]byte
	_, _, errno := syscall.RawSyscall(syscall.SYS_SIGACTION,
		uintptr(s), uintptr(unsafe.Pointer(&act[0])), 0)
	if errno != 0 {
		signal.Reset(sig)
	}
}

// setOSIgnore installs SIG_IGN for sig at the OS level via a raw sigaction
// syscall, WITHOUT going through os/signal.Ignore. See the linux build for
// the full rationale (VSC-PCTS TP714).
func setOSIgnore(sig os.Signal) {
	s, ok := sig.(syscall.Signal)
	if !ok {
		signal.Ignore(sig)
		return
	}
	var act [16]byte
	*(*uintptr)(unsafe.Pointer(&act[0])) = 1 // SIG_IGN
	_, _, errno := syscall.RawSyscall(syscall.SYS_SIGACTION,
		uintptr(s), uintptr(unsafe.Pointer(&act[0])), 0)
	if errno != 0 {
		signal.Ignore(sig)
	}
}

// osSignalIgnored reports whether the signal's real OS disposition is
// currently SIG_IGN. See the linux build for the full rationale
// (VSC-PCTS TP720).
func osSignalIgnored(sig os.Signal) bool {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return signal.Ignored(sig)
	}
	var oldAct [16]byte
	_, _, errno := syscall.RawSyscall(syscall.SYS_SIGACTION,
		uintptr(s), 0, uintptr(unsafe.Pointer(&oldAct[0])))
	if errno != 0 {
		return false
	}
	return *(*uintptr)(unsafe.Pointer(&oldAct[0])) == 1 // SIG_IGN
}
