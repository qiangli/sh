// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package pathconv

import (
	"runtime"
	"strings"
)

// NativeArgs spells, on Windows, the arguments a native program cannot read in
// the shell's POSIX form: an argument that is a drive path of a present drive
// ("/c", "/c/Users/x", "/mnt/c/x"), or an option whose value is one
// ("--git-dir=/c/x"), becomes "C:\Users\x". Everything else is unchanged, and
// off Windows it is the identity.
//
// It is for the programs bashy provisions and runs itself (binmgr-managed
// tools, the POSIX provider binaries), never for every exec: the shell keeps
// its own spelling everywhere else, so Bash# and scripts see no change.
func NativeArgs(args []string) []string {
	if runtime.GOOS != "windows" {
		return args
	}
	return NativeArgsIn(LogicalDrives(), args)
}

// NativeArgsIn is [NativeArgs] against an explicit set of present drives,
// applied regardless of the host OS (tests, planning).
func NativeArgsIn(drives DriveSet, args []string) []string {
	var out []string
	for i, arg := range args {
		native := nativeArg(drives, arg)
		if native == arg {
			continue
		}
		if out == nil {
			out = append([]string(nil), args...)
		}
		out[i] = native
	}
	if out == nil {
		return args
	}
	return out
}

func nativeArg(drives DriveSet, arg string) string {
	if strings.HasPrefix(arg, "-") {
		if eq := strings.IndexByte(arg, '='); eq > 0 {
			if v := nativeDrive(drives, arg[eq+1:]); v != arg[eq+1:] {
				return arg[:eq+1] + v
			}
		}
		return arg
	}
	return nativeDrive(drives, arg)
}

func nativeDrive(drives DriveSet, value string) string {
	drive, rest, ok := DrivePathIn(drives, value)
	if !ok {
		return value
	}
	return string(drive) + ":" + strings.ReplaceAll(rest, "/", `\`)
}
