// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build !unix

package interp

import (
	"context"
	"os"
	"time"
)

func fdReadableNow(f *os.File) bool {
	return false
}

type timeoutFileReader struct {
	ctx      context.Context
	file     *os.File
	deadline time.Time
}

func (r *timeoutFileReader) Read(p []byte) (int, error) {
	return r.file.Read(p)
}
