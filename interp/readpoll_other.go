// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build !unix

package interp

import "os"

func fdReadableNow(f *os.File) bool {
	return false
}
