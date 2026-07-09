// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build !darwin && !linux

package interp

import (
	"os"
	"os/signal"
)

func restoreExecSignal(sig os.Signal) {
	signal.Reset(sig)
}
