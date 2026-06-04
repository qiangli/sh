// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build linux || solaris

package interp

import (
	"syscall"
	"time"
)

func unixStatMtime(s *syscall.Stat_t) time.Time {
	return time.Unix(s.Mtim.Sec, s.Mtim.Nsec)
}

func unixStatAtime(s *syscall.Stat_t) time.Time {
	return time.Unix(s.Atim.Sec, s.Atim.Nsec)
}
