// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build !unix

package interp

func forwardExecReplacementSignals(pid int) func() { return func() {} }
