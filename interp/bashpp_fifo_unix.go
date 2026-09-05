// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build unix

package interp

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Acquire without ever entering a blocking open. The read probe pins the
// inode and keeps writer acquisition nonblocking, but is not a registered
// reader and therefore cannot itself satisfy the interpreter rendezvous.
func bashPPFIFOAcquire(ctx context.Context, dirFile *os.File, dir, path string, flags int) (file, probe *os.File, key bashPPFIFOIdentity, fifo bool, err error) {
	dirFD := unix.AT_FDCWD
	if !shellPathAbs(path) {
		if dirFile == nil {
			return nil, nil, key, false, nil
		}
		dirFD = int(dirFile.Fd())
	}
	var before unix.Stat_t
	if err := bashPPFIFORetry(ctx, func() error { return unix.Fstatat(dirFD, path, &before, 0) }); err != nil || before.Mode&unix.S_IFMT != unix.S_IFIFO {
		if ctx.Err() != nil {
			return nil, nil, key, true, ctx.Err()
		}
		return nil, nil, key, false, nil
	}
	key = bashPPFIFOIdentity{uint64(before.Dev), uint64(before.Ino)}
	open := func(mode int) (*os.File, error) {
		var fd int
		err := bashPPFIFORetry(ctx, func() (err error) {
			fd, err = unix.Openat(dirFD, path, mode|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			return err
		})
		if err != nil {
			return nil, err
		}
		var st unix.Stat_t
		if err = bashPPFIFORetry(ctx, func() error { return unix.Fstat(fd, &st) }); err != nil || st.Mode&unix.S_IFMT != unix.S_IFIFO || uint64(st.Dev) != key.dev || uint64(st.Ino) != key.ino {
			_ = unix.Close(fd)
			if err == nil {
				err = fmt.Errorf("FIFO changed while acquiring its rendezvous descriptor")
			}
			return nil, err
		}
		return os.NewFile(uintptr(fd), shellPathJoinAbs(dir, path)), nil
	}
	probe, err = open(unix.O_RDONLY)
	if err != nil {
		return nil, nil, key, true, fmt.Errorf("FIFO rendezvous requires a readable inode: %w", err)
	}
	if flags&(os.O_WRONLY|os.O_RDWR) == 0 {
		return probe, nil, key, true, nil
	}
	file, err = open(flags & (os.O_WRONLY | os.O_RDWR | os.O_APPEND))
	if err != nil {
		_ = probe.Close()
		return nil, nil, key, true, err
	}
	return file, probe, key, true, nil
}

// All callers use nonblocking operations. Signals may interrupt them, but
// retries cannot override cancellation or introduce a blocking-open join.
func bashPPFIFORetry(ctx context.Context, op func() error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := op(); err != unix.EINTR {
			return err
		}
	}
}
