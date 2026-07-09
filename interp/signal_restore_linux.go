// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build linux

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
	var act [128]byte
	_, _, errno := syscall.RawSyscall6(syscall.SYS_RT_SIGACTION,
		uintptr(s), uintptr(unsafe.Pointer(&act[0])), 0, linuxSigsetSize, 0, 0)
	if errno != 0 {
		signal.Reset(sig)
	}
}
