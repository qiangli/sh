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
