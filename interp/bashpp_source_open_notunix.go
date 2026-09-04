// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build !unix

package interp

import (
	"context"
	"os"
)

func bashPPTaskProbeOpen(dirFile *os.File, dir, path string, flags int, mode os.FileMode) (*os.File, bool, error) {
	return nil, false, nil
}

func bashPPTaskSourceClearNonblock(file *os.File) error { return nil }

type bashPPTaskFifoIdentity struct{}

func bashPPTaskFifoIdentityOf(file *os.File) (bashPPTaskFifoIdentity, error) {
	return bashPPTaskFifoIdentity{}, nil
}

func bashPPTaskFifoOpen(ctx context.Context, dirFile *os.File, dir, path string, flags int, mode os.FileMode) (*os.File, error) {
	return nil, os.ErrInvalid
}
