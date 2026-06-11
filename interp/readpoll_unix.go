// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"os"

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
