// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import "testing"

func TestShellPathWindowsRootTranslation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dir         string
		path        string
		wantAbs     bool
		wantOS      string
		wantAbsPath string
	}{
		{
			name:        "slash root uses current drive",
			dir:         `C:\work\repo`,
			path:        `/Windows/System32`,
			wantAbs:     true,
			wantOS:      `C:\Windows\System32`,
			wantAbsPath: `C:\Windows\System32`,
		},
		{
			name:        "backslash root uses current drive",
			dir:         `D:\work`,
			path:        `\tmp\file`,
			wantAbs:     true,
			wantOS:      `D:\tmp\file`,
			wantAbsPath: `D:\tmp\file`,
		},
		{
			name:        "drive absolute remains unchanged",
			dir:         `C:\work`,
			path:        `D:\bin\tool.exe`,
			wantAbs:     true,
			wantOS:      `D:\bin\tool.exe`,
			wantAbsPath: `D:\bin\tool.exe`,
		},
		{
			name:        "relative joins with cwd",
			dir:         `C:\work`,
			path:        `bin\tool.exe`,
			wantAbs:     false,
			wantOS:      `bin\tool.exe`,
			wantAbsPath: `C:\work\bin\tool.exe`,
		},
		{
			name:        "missing drive defaults to c",
			dir:         `/work`,
			path:        `/bin/sh`,
			wantAbs:     true,
			wantOS:      `C:\bin\sh`,
			wantAbsPath: `C:\bin\sh`,
		},
		{
			name:        "msys drive /c maps to C:",
			dir:         `C:\work`,
			path:        `/c/Users/Lern`,
			wantAbs:     true,
			wantOS:      `C:\Users\Lern`,
			wantAbsPath: `C:\Users\Lern`,
		},
		{
			name:        "msys drive /d maps to D: regardless of cwd",
			dir:         `C:\work`,
			path:        `/d/foo/bar`,
			wantAbs:     true,
			wantOS:      `D:\foo\bar`,
			wantAbsPath: `D:\foo\bar`,
		},
		{
			name:        "msys bare /c is the drive root",
			dir:         `C:\work`,
			path:        `/c`,
			wantAbs:     true,
			wantOS:      `C:\`,
			wantAbsPath: `C:\`,
		},
		{
			name:        "single-letter dir under root is not a drive ref",
			dir:         `C:\work`,
			path:        `/bin`,
			wantAbs:     true,
			wantOS:      `C:\bin`,
			wantAbsPath: `C:\bin`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellPathAbsMode(tt.path, true); got != tt.wantAbs {
				t.Fatalf("wrong absolute result: want %v, got %v", tt.wantAbs, got)
			}
			if got := shellPathToOSMode(tt.dir, tt.path, true); got != tt.wantOS {
				t.Fatalf("wrong os path:\nwant: %q\ngot:  %q", tt.wantOS, got)
			}
			if got := shellPathJoinAbsMode(tt.dir, tt.path, true); got != tt.wantAbsPath {
				t.Fatalf("wrong absolute path:\nwant: %q\ngot:  %q", tt.wantAbsPath, got)
			}
		})
	}
}

func TestShellPathPosixTranslation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dir  string
		path string
		want string
	}{
		{name: "absolute", dir: "/work", path: "/bin/sh", want: "/bin/sh"},
		{name: "relative", dir: "/work", path: "bin/sh", want: "/work/bin/sh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellPathJoinAbsMode(tt.dir, tt.path, false); got != tt.want {
				t.Fatalf("wrong path:\nwant: %q\ngot:  %q", tt.want, got)
			}
		})
	}
}
