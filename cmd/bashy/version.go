// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import "os"

const (
	bashVersion = "5.3.0(1)-bashy"

	bashVerMajor = "5"
	bashVerMinor = "3"
	bashVerPatch = "0"
)

// bashVersionVars returns the environment variables that identify bashy
// as a Bash 5.3 compatible shell.
func bashVersionVars() []string {
	exe, _ := os.Executable()
	if exe == "" {
		exe = "bashy"
	}
	return []string{
		"BASH=" + exe,
		"BASH_VERSION=" + bashVersion,
	}
}
