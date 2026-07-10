//go:build darwin

// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

package interp

import (
	"syscall"
	"unsafe"
)

func startupIgnoredSignals(env string) map[string]bool {
	ignored := parseHardIgnore(env)
	for _, e := range sortedSignalEntries() {
		if e.Sig == 0 || e.Name == "KILL" || e.Name == "STOP" {
			continue
		}
		// A backgrounded non-interactive harness may launch us with SIGINT
		// ignored. Treat only the explicit bashy bridge as a hard ignore so
		// bare `trap` output matches bash's fixtures.
		if e.Name == "INT" {
			continue
		}
		var act [16]byte
		_, _, errno := syscall.RawSyscall(syscall.SYS_SIGACTION,
			uintptr(e.Sig), 0, uintptr(unsafe.Pointer(&act[0])))
		if errno != 0 {
			continue
		}
		if *(*uintptr)(unsafe.Pointer(&act[0])) == 1 {
			ignored = mergeStartupIgnored(ignored, e.Name)
		}
	}
	return ignored
}
