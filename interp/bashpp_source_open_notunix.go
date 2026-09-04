// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build !unix

package interp

import "os"

func bashPPTaskProbeOpen(dir, path string, flags int, mode os.FileMode) (*os.File, bool, error) {
	return nil, false, nil
}

func bashPPTaskSourceOpenFlags(flags int) (int, bool) { return flags, false }

func bashPPTaskSourceClearNonblock(file *os.File) error { return nil }
