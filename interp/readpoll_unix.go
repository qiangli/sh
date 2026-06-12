// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func fdReadableNow(f *os.File) bool {
	pollFd := []unix.PollFd{{
		Fd:     int32(f.Fd()),
		Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR,
	}}
	n, err := unix.Poll(pollFd, 0)
	return err == nil && n > 0 && pollFd[0].Revents != 0
}

type timeoutFileReader struct {
	ctx      context.Context
	file     *os.File
	deadline time.Time
}

func (r *timeoutFileReader) Read(p []byte) (int, error) {
	for {
		if err := r.ctx.Err(); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return 0, os.ErrDeadlineExceeded
			}
			return 0, err
		}
		timeout := time.Until(r.deadline)
		if timeout <= 0 {
			return 0, os.ErrDeadlineExceeded
		}
		usec := timeout.Microseconds()
		if usec == 0 {
			usec = 1
		}
		fd := int(r.file.Fd())
		if fd < 0 {
			return 0, os.ErrInvalid
		}
		if fd >= unix.FD_SETSIZE {
			msec := int(timeout / time.Millisecond)
			if msec == 0 {
				msec = 1
			}
			pollFd := []unix.PollFd{{
				Fd:     int32(fd),
				Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR,
			}}
			n, err := unix.Poll(pollFd, msec)
			if err == unix.EINTR {
				continue
			}
			if err != nil {
				return 0, err
			}
			if n == 0 || pollFd[0].Revents == 0 {
				continue
			}
			return r.file.Read(p)
		}
		var rfds unix.FdSet
		rfds.Set(fd)
		tv := unix.Timeval{
			Sec:  usec / 1e6,
			Usec: int32(usec % 1e6),
		}
		n, err := unix.Select(fd+1, &rfds, nil, nil, &tv)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n == 0 || !rfds.IsSet(fd) {
			continue
		}
		return r.file.Read(p)
	}
}
