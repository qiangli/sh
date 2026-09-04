// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build unix

package interp

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

func bashPPTaskProbeOpen(dirFile *os.File, dir, path string, flags int, mode os.FileMode) (*os.File, bool, error) {
	openFlags := flags | unix.O_NONBLOCK | unix.O_CLOEXEC
	var fd int
	var err error
	if shellPathAbs(path) {
		fd, err = unix.Open(path, openFlags, uint32(mode))
	} else if dirFile == nil {
		return nil, true, &os.PathError{Op: "open", Path: path, Err: unix.EBADF}
	} else {
		fd, err = unix.Openat(int(dirFile.Fd()), path, openFlags, uint32(mode))
	}
	if err != nil {
		// In particular, fail closed on ENXIO from a FIFO writer without a
		// reader. Path-only classification could race a FIFO replacement and
		// would arm later tasks without having acquired the intended object.
		return nil, true, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), shellPathJoinAbs(dir, path)), true, nil
}

func bashPPTaskSourceClearNonblock(file *os.File) error {
	return unix.SetNonblock(int(file.Fd()), false)
}

// bashPPTaskFifoIdentity names the object a probe open acquired, so a
// re-open can prove it reached the same one.
type bashPPTaskFifoIdentity struct {
	dev uint64
	ino uint64
}

func bashPPTaskFifoIdentityOf(file *os.File) (bashPPTaskFifoIdentity, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &st); err != nil {
		return bashPPTaskFifoIdentity{}, err
	}
	return bashPPTaskFifoIdentity{dev: uint64(st.Dev), ino: uint64(st.Ino)}, nil
}

// bashPPTaskFifoOpen performs a FIFO's REAL open for a task, blocking on the
// rendezvous and honouring cancellation.
//
// It exists because the probe open cannot be the final descriptor. The probe
// carries O_NONBLOCK so a task never blocks before it has armed — but for a
// FIFO that flag does not defer the wait, it CANCELS it: an O_RDONLY
// O_NONBLOCK open succeeds with no writer present, and the descriptor it
// returns reads end-of-file instead of waiting for one. Clearing O_NONBLOCK
// afterwards cannot recover a rendezvous that already did not happen.
//
// So the probe classifies and the re-open commits: arm first, then open for
// real. The block is the open, which is exactly what "arm before block"
// promises. Relative paths resolve against the task's RETAINED directory, the
// same as the probe, so a concurrently renamed working directory still
// resolves to the object the task started with.
func bashPPTaskFifoOpen(ctx context.Context, dirFile *os.File, dir, path string, flags int, mode os.FileMode) (*os.File, error) {
	open := func(openFlags int, perm uint32) (int, error) {
		if shellPathAbs(path) {
			return unix.Open(path, openFlags, perm)
		}
		if dirFile == nil {
			return -1, unix.EBADF
		}
		return unix.Openat(int(dirFile.Fd()), path, openFlags, perm)
	}
	rwc, err := openFifoWithContextFunc(ctx, shellPathJoinAbs(dir, path), flags, mode, open)
	if err != nil {
		return nil, err
	}
	file, ok := rwc.(*os.File)
	if !ok {
		_ = rwc.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: unix.EBADF}
	}
	return file, nil
}
