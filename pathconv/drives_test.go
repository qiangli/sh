// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package pathconv

import "testing"

func TestDriveSet(t *testing.T) {
	t.Parallel()

	set := DrivesOf("cD")
	for _, tt := range []struct {
		drive byte
		want  bool
	}{
		{'c', true}, {'C', true}, {'d', true}, {'D', true},
		{'a', false}, {'e', false}, {'z', false}, {'1', false}, {'/', false}, {0, false},
	} {
		if got := set.Has(tt.drive); got != tt.want {
			t.Errorf("DrivesOf(cD).Has(%q) = %v, want %v", tt.drive, got, tt.want)
		}
	}
	if DrivesOf("") != 0 || DrivesOf("1:/") != 0 {
		t.Error("non-letters made a drive")
	}
	for c := byte('A'); c <= 'Z'; c++ {
		if !AllDrives.Has(c) || !AllDrives.Has(c|0x20) {
			t.Errorf("AllDrives lacks %q", c)
		}
	}
	if AllDrives.Has('[') || AllDrives.Has('@') {
		t.Error("AllDrives has a non-letter")
	}
	// GetLogicalDrives' bit layout: bit 0 is A:.
	if got := DrivesOf("AC"); got != 0b101 {
		t.Errorf("DrivesOf(AC) = %b, want 101", got)
	}
}

// MSYS converts /x/… to X:\… only when X: is a logical drive; with the
// drive set pinned, /a/b/c on a host without an A: drive is a POSIX path
// (varenv.tests: HOME=/a/b/c reaches the child as /a/b/c, not A:\b\c).
func TestDrivePathHonoursLogicalDrives(t *testing.T) {
	// Not parallel: pins the LogicalDrives and TempDir hooks.
	oldDrives, oldTempDir := LogicalDrives, TempDir
	LogicalDrives = func() DriveSet { return DrivesOf("CD") }
	TempDir = func() string { return `C:\Temp` }
	defer func() { LogicalDrives, TempDir = oldDrives, oldTempDir }()

	for _, tt := range []struct {
		path      string
		wantDrive byte
		wantOK    bool
	}{
		{`/c/Users`, 'C', true},
		{`/d`, 'D', true},
		{`/D/x`, 'D', true},
		{`\c\x`, 'C', true},
		{`/mnt/c/x`, 'C', true},
		{`/a/b/c`, 0, false},
		{`/a`, 0, false},
		{`/mnt/a/b`, 0, false},
		{`/e/x`, 0, false},
	} {
		drive, _, ok := DrivePath(tt.path)
		if drive != tt.wantDrive || ok != tt.wantOK {
			t.Errorf("DrivePath(%q) = (%q, %v), want (%q, %v)", tt.path, drive, ok, tt.wantDrive, tt.wantOK)
		}
	}
	// The explicit-set form ignores the hook.
	if _, _, ok := DrivePathIn(AllDrives, `/a/b/c`); !ok {
		t.Error("DrivePathIn(AllDrives, /a/b/c) did not convert")
	}
	if _, _, ok := DrivePathIn(0, `/c/x`); ok {
		t.Error("DrivePathIn(none, /c/x) converted")
	}

	// The conversions: an absent drive falls through to the POSIX rules.
	for _, tt := range []struct{ in, want string }{
		{`/a/b/c`, `/a/b/c`},
		{`/mnt/a/b`, `/mnt/a/b`},
		{`/c/Users/me`, `C:\Users\me`},
		{`/d/x`, `D:\x`},
	} {
		if got := NativePath(tt.in); got != tt.want {
			t.Errorf("NativePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := NativePathList(`/a/b:/c/x:/tmp`); got != `/a/b;C:\x;/tmp` {
		t.Errorf("NativePathList = %q", got)
	}
	m := NewMounts(`D:\w\root`, nil, `C:\Temp`)
	for _, tt := range []struct {
		m    *Mounts
		in   string
		want string
	}{
		// Without a root the drive-relative fallback prepends dir's volume.
		{nil, `/a/b/c`, `C:\a\b\c`},
		{nil, `/c/x`, `C:\x`},
		{nil, `/mnt/a/b`, `C:\mnt\a\b`},
		// With one, /a is a directory under the root, as MSYS spells it.
		{m, `/a/b/c`, `D:\w\root\a\b\c`},
		{m, `/c/x`, `C:\x`},
		{m, `/d`, `D:\`},
	} {
		if got := ToOSMountsMode(tt.m, `C:\work`, tt.in, true); got != tt.want {
			t.Errorf("ToOSMountsMode(%v, %q) = %q, want %q", tt.m != nil, tt.in, got, tt.want)
		}
	}
	if got := NativePathMounts(m, `/a/b/c`); got != `D:\w\root\a\b\c` {
		t.Errorf("NativePathMounts(root, /a/b/c) = %q", got)
	}
	// FromOS is the inverse for present drives only and is unaffected.
	if got := FromOSMode(`A:\b\c`, true); got != `/a/b/c` {
		t.Errorf("FromOSMode(A:\\b\\c) = %q", got)
	}
}
