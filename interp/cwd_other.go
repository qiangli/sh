// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build !unix

package interp

import "os"

func openRunnerDir(path string) (*os.File, error) {
	return nil, nil
}

func dupRunnerDir(file *os.File) (*os.File, error) {
	return nil, nil
}

func runnerExecDir(r *Runner, fallback string) string {
	return fallback
}
