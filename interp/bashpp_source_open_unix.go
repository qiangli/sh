// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build unix

package interp

import (
	"os"
	"syscall"
)

func bashPPTaskProbeOpen(dir, path string, flags int, mode os.FileMode) (*os.File, bool, error) {
	path = shellPathJoinAbs(dir, path)
	fd, err := syscall.Open(path, flags|syscall.O_NONBLOCK|syscall.O_CLOEXEC, uint32(mode))
	if err != nil {
		// In particular, fail closed on ENXIO from a FIFO writer without a
		// reader. Path-only classification could race a FIFO replacement and
		// would arm later tasks without having acquired the intended object.
		return nil, true, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), true, nil
}

// O_NONBLOCK makes pathname replacement with a FIFO harmless: open returns
// without waiting, then the caller validates the opened descriptor itself.
func bashPPTaskSourceOpenFlags(flags int) (int, bool) { return flags | syscall.O_NONBLOCK, true }

func bashPPTaskSourceClearNonblock(file *os.File) error {
	return syscall.SetNonblock(int(file.Fd()), false)
}
