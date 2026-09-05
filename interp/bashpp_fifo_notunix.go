// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build !unix

package interp

import (
	"context"
	"os"
)

func bashPPFIFOAcquire(ctx context.Context, dirFile *os.File, dir, path string, flags int) (*os.File, *os.File, bashPPFIFOIdentity, bool, error) {
	return nil, nil, bashPPFIFOIdentity{}, false, nil
}
