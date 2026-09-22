// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"slices"
	"testing"

	"mvdan.cc/sh/v3/pathconv"
)

// varenv.tests: `HOME=/a/b/c /bin/echo $HOME` must hand the child /a/b/c.
// A single-letter first component is a drive only when that drive is
// present; with the drive set pinned to C: and D:, /a/b/c is a POSIX path
// while /c/… and /d/… still convert, in the built-in path variables, PATH
// and the BASHYENV opt-ins alike.
func TestNativeExecEnvHonoursLogicalDrives(t *testing.T) {
	// Not parallel: pins the LogicalDrives hook.
	old := pathconv.LogicalDrives
	pathconv.LogicalDrives = func() pathconv.DriveSet { return pathconv.DrivesOf("CD") }
	defer func() { pathconv.LogicalDrives = old }()

	env := []string{
		"HOME=/a/b/c",
		"TMPDIR=/mnt/a/tmp",
		"USERPROFILE=/c/Users/me",
		"PATH=/a/bin:/c/Go/bin:/d/tools",
		"BASHYENV=X/p:Y/l",
		"X=/e/x",
		"Y=/c/a:/z/b",
	}
	want := []string{
		"HOME=/a/b/c",
		"TMPDIR=/mnt/a/tmp",
		`USERPROFILE=C:\Users\me`,
		`PATH=/a/bin;C:\Go\bin;D:\tools`,
		"BASHYENV=X/p:Y/l",
		"X=/e/x",
		`Y=C:\a;/z/b`,
	}
	if got := nativeExecEnvMountsMode(nil, slices.Clone(env), true); !slices.Equal(got, want) {
		t.Errorf("without mounts = %q\nwant %q", got, want)
	}
	// With a virtual root, /a/b/c is a directory under it — what MSYS
	// hands a native child — and the present drives are unchanged.
	m := pathconv.NewMounts(`D:\w\root`, nil, `C:\Temp`)
	want = []string{
		`HOME=D:\w\root\a\b\c`,
		`TMPDIR=D:\w\root\mnt\a\tmp`,
		`USERPROFILE=C:\Users\me`,
		`PATH=D:\w\root\a\bin;C:\Go\bin;D:\tools`,
		"BASHYENV=X/p:Y/l",
		"X=/e/x",
		`Y=C:\a;/z/b`,
	}
	if got := nativeExecEnvMountsMode(m, slices.Clone(env), true); !slices.Equal(got, want) {
		t.Errorf("with mounts = %q\nwant %q", got, want)
	}
	// The shell's own conversion of a present drive keeps working.
	if got := shellPathToOSMode(`C:\work`, "/c/Users/me", true); got != `C:\Users\me` {
		t.Errorf("ToOS(/c/Users/me) = %q", got)
	}
	if got := shellPathToOSMode(`C:\work`, "/a/b/c", true); got != `C:\a\b\c` {
		t.Errorf("ToOS(/a/b/c) without an A: drive = %q, want the drive-relative fallback", got)
	}
}
