// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package pathconv

import "testing"

func TestIsAbsModeWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{`C:\Users\x`, true},
		{`C:/Users/x`, true},
		{`/c/Users/x`, true},
		{`/mnt/c/Users/x`, true},
		{`/foo`, true},
		{`\foo`, true},
		{`foo`, false},
		{`foo/bar`, false},
		{``, false},
	}
	for _, tt := range tests {
		if got := IsAbsMode(tt.path, true); got != tt.want {
			t.Errorf("IsAbsMode(%q, windows) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestToOSModeWindows(t *testing.T) {
	// Not parallel: pins the TempDir hook.
	oldTempDir := TempDir
	TempDir = func() string { return `C:\Users\me\AppData\Local\Temp` }
	defer func() { TempDir = oldTempDir }()

	tests := []struct {
		name string
		dir  string
		path string
		want string
	}{
		{name: "drive backslash", dir: `C:\work`, path: `D:\bin\tool`, want: `D:\bin\tool`},
		{name: "drive forward slash", dir: `C:\work`, path: `D:/bin/tool`, want: `D:\bin\tool`},
		{name: "msys drive", dir: `C:\work`, path: `/d/foo`, want: `D:\foo`},
		{name: "msys bare drive", dir: `C:\work`, path: `/d`, want: `D:\`},
		{name: "wsl mount", dir: `C:\work`, path: `/mnt/d/foo/bar`, want: `D:\foo\bar`},
		{name: "wsl bare mount", dir: `C:\work`, path: `/mnt/d`, want: `D:\`},
		{name: "wsl uppercase drive", dir: `C:\work`, path: `/mnt/D/foo`, want: `D:\foo`},
		{name: "bare /mnt is not a drive", dir: `C:\work`, path: `/mnt`, want: `C:\mnt`},
		{name: "/mnt/ with no letter", dir: `C:\work`, path: `/mnt/`, want: `C:\mnt\`},
		{name: "/mnt-prefixed word", dir: `C:\work`, path: `/mntx/foo`, want: `C:\mntx\foo`},
		{name: "drive-relative root", dir: `D:\work`, path: `/foo`, want: `D:\foo`},
		{name: "relative unchanged", dir: `C:\work`, path: `bin/tool`, want: `bin/tool`},
		{name: "dev null", dir: `C:\work`, path: `/dev/null`, want: `NUL`},
		{name: "dev tcp untouched", dir: `C:\work`, path: `/dev/tcp/h/80`, want: `C:\dev\tcp\h\80`},
		{name: "tmp root", dir: `C:\work`, path: `/tmp`, want: `C:\Users\me\AppData\Local\Temp`},
		{name: "tmp file", dir: `C:\work`, path: `/tmp/x.log`, want: `C:\Users\me\AppData\Local\Temp\x.log`},
		{name: "tmp-prefixed word untouched", dir: `C:\work`, path: `/tmpdir/x`, want: `C:\tmpdir\x`},
		{name: "native backslash tmp is drive-relative", dir: `D:\work`, path: `\tmp\x`, want: `D:\tmp\x`},
		{name: "device path passthrough", dir: `C:\work`, path: `\\.\pipe\sh-np-1`, want: `\\.\pipe\sh-np-1`},
		{name: "unc passthrough", dir: `C:\work`, path: `//server/share`, want: `//server/share`},
	}
	for _, tt := range tests {
		if got := ToOSMode(tt.dir, tt.path, true); got != tt.want {
			t.Errorf("%s: ToOSMode(%q, %q, windows) = %q, want %q", tt.name, tt.dir, tt.path, got, tt.want)
		}
	}

	// Non-windows mode is the identity.
	for _, p := range []string{`/tmp/x`, `/dev/null`, `/mnt/c/x`, `C:\x`, `rel/path`} {
		if got := ToOSMode(`/work`, p, false); got != p {
			t.Errorf("ToOSMode(%q, posix) = %q, want unchanged", p, got)
		}
	}
}

func TestToSlashModeWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		dir  string
		path string
		want string
	}{
		{`C:\work`, `C:\Users\x`, `C:/Users/x`},
		{`C:\work`, `/c/Users/x`, `C:/Users/x`},
		{`C:\work`, `/mnt/d/data`, `D:/data`},
		{`D:\work`, `/foo`, `D:/foo`},
	}
	for _, tt := range tests {
		if got := ToSlashMode(tt.dir, tt.path, true); got != tt.want {
			t.Errorf("ToSlashMode(%q, %q, windows) = %q, want %q", tt.dir, tt.path, got, tt.want)
		}
	}
	if got := ToSlashMode(`/work`, `/c/x`, false); got != `/c/x` {
		t.Errorf("ToSlashMode posix = %q, want unchanged", got)
	}
}

func TestDrivePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path      string
		wantDrive byte
		wantRest  string
		wantOK    bool
	}{
		{`/c`, 'C', `/`, true},
		{`/c/Users`, 'C', `/Users`, true},
		{`\d\work`, 'D', `/work`, true},
		{`/mnt/c`, 'C', `/`, true},
		{`/mnt/e/data/x`, 'E', `/data/x`, true},
		{`/mnt`, 0, ``, false},
		{`/mnt/`, 0, ``, false},
		{`/mnt/cc`, 0, ``, false},
		{`/cc/x`, 0, ``, false},
		{`C:\x`, 0, ``, false},
		{`rel`, 0, ``, false},
	}
	for _, tt := range tests {
		drive, rest, ok := DrivePath(tt.path)
		if drive != tt.wantDrive || rest != tt.wantRest || ok != tt.wantOK {
			t.Errorf("DrivePath(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.path, drive, rest, ok, tt.wantDrive, tt.wantRest, tt.wantOK)
		}
	}
}

func TestDriveOf(t *testing.T) {
	t.Parallel()

	if d, ok := DriveOf(`c:\work`); !ok || d != 'C' {
		t.Errorf(`DriveOf(c:\work) = (%q, %v), want ('C', true)`, d, ok)
	}
	if d, ok := DriveOf(`D:/x`); !ok || d != 'D' {
		t.Errorf(`DriveOf(D:/x) = (%q, %v), want ('D', true)`, d, ok)
	}
	for _, p := range []string{`/c/x`, `\\server\x`, `rel`, ``} {
		if _, ok := DriveOf(p); ok {
			t.Errorf("DriveOf(%q) ok, want not a drive path", p)
		}
	}
}

func TestNativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  string
	}{
		{`/c/Users/x`, `C:\Users\x`},
		{`/mnt/d/data`, `D:\data`},
		{`/c`, `C:\`},
		{`/usr/bin`, `/usr/bin`},
		{`C:\x`, `C:\x`},
		{`\c\x`, `\c\x`},
		{`/c/a:/c/b`, `/c/a:/c/b`},
		{``, ``},
	}
	for _, tt := range tests {
		if got := NativePath(tt.value); got != tt.want {
			t.Errorf("NativePath(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestNativePathList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  string
	}{
		{`/c/a:/d/b`, `C:\a;D:\b`},
		{`/mnt/c/a:/mnt/d/b`, `C:\a;D:\b`},
		{`/c/a`, `C:\a`},
		{`C:\a;/c/b`, `C:\a;C:\b`},
		{`C:\Go\bin`, `C:\Go\bin`},         // ':' split would mangle; single-letter guard
		{`a:b`, `a:b`},                     // ditto
		{`/c/a:/usr/bin`, `C:\a;/usr/bin`}, // non-drive elements pass through
		{``, ``},
	}
	for _, tt := range tests {
		if got := NativePathList(tt.value); got != tt.want {
			t.Errorf("NativePathList(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestFromOSModeWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want string
	}{
		{`C:\Users\x`, `/c/Users/x`},
		{`C:/Users/x`, `/c/Users/x`},
		{`d:`, `/d/`},
		{`\\server\share`, `//server/share`},
		{`rel\path`, `rel/path`},
	}
	for _, tt := range tests {
		if got := FromOSMode(tt.path, true); got != tt.want {
			t.Errorf("FromOSMode(%q, windows) = %q, want %q", tt.path, got, tt.want)
		}
	}
	if got := FromOSMode(`C:\x`, false); got != `C:\x` {
		t.Errorf("FromOSMode posix = %q, want unchanged", got)
	}
}

func TestJoinAbsModeWindows(t *testing.T) {
	// Not parallel: pins the TempDir hook.
	oldTempDir := TempDir
	TempDir = func() string { return `C:\Temp` }
	defer func() { TempDir = oldTempDir }()

	tests := []struct {
		dir  string
		path string
		want string
	}{
		{`C:\work`, `/mnt/d/x`, `D:\x`},
		{`/c/work`, `rel`, `C:\work\rel`},
		{`C:\work`, `/tmp/x`, `C:\Temp\x`},
		{`C:\work`, `/dev/null`, `NUL`},
	}
	for _, tt := range tests {
		if got := JoinAbsMode(tt.dir, tt.path, true); got != tt.want {
			t.Errorf("JoinAbsMode(%q, %q, windows) = %q, want %q", tt.dir, tt.path, got, tt.want)
		}
	}
}
