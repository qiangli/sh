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
