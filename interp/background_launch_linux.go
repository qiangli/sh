// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build linux

package interp

import "time"

func yieldExternalLaunch() {
	// The external child is created by a different Go thread than the one
	// interpreting the parent list. Give the kernel one minimum timer turn so
	// the child gets the same practical first-run opportunity it has after a
	// traditional shell fork. This does not wait for child completion.
	time.Sleep(time.Millisecond)
}
