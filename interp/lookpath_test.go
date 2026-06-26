// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"mvdan.cc/sh/v3/expand"
)

func TestLookPathDirWindowsMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		env      expand.Environ
		file     string
		found    string
		want     string
		wantTry  []string
		wantExts []string
	}{
		{
			name:     "slash path skips PATH",
			env:      expand.ListEnviron("PATH=C:\\bin;D:/tools"),
			file:     `bin/rpc-server`,
			found:    `bin/rpc-server.exe`,
			want:     `bin/rpc-server.exe`,
			wantTry:  []string{`bin/rpc-server`},
			wantExts: []string{".com", ".exe", ".bat", ".cmd"},
		},
		{
			name:     "backslash path skips PATH",
			env:      expand.ListEnviron("PATH=C:\\bin;D:/tools"),
			file:     `bin\rpc-server`,
			found:    `bin\rpc-server.exe`,
			want:     `bin\rpc-server.exe`,
			wantTry:  []string{`bin\rpc-server`},
			wantExts: []string{".com", ".exe", ".bat", ".cmd"},
		},
		{
			name:     "drive path skips PATH",
			env:      expand.ListEnviron("PATH=C:\\bin;D:/tools"),
			file:     `C:\bin\rpc-server`,
			found:    `C:\bin\rpc-server.exe`,
			want:     `C:\bin\rpc-server.exe`,
			wantTry:  []string{`C:\bin\rpc-server`},
			wantExts: []string{".com", ".exe", ".bat", ".cmd"},
		},
		{
			name:     "PATH search appends requested PATHEXT",
			env:      expand.ListEnviron("PATH=C:\\bin;D:/tools", "PATHEXT=EXE;.BAT"),
			file:     `rpc-server`,
			found:    `D:/tools\rpc-server.exe`,
			want:     `D:/tools\rpc-server.exe`,
			wantTry:  []string{`C:\bin\rpc-server`, `D:/tools\rpc-server`},
			wantExts: []string{".exe", ".bat"},
		},
		{
			name:     "empty PATH entry searches dot",
			env:      expand.ListEnviron("PATH=;C:\\bin"),
			file:     `rpc-server`,
			found:    `.\rpc-server.exe`,
			want:     `.\rpc-server.exe`,
			wantTry:  []string{`.\rpc-server`},
			wantExts: []string{".com", ".exe", ".bat", ".cmd"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotTry []string
			var gotExts []string
			find := func(_ string, file string, exts []string) (string, error) {
				gotTry = append(gotTry, file)
				gotExts = append([]string(nil), exts...)
				if file == trimWindowsExt(tt.found, exts) {
					return tt.found, nil
				}
				return "", os.ErrNotExist
			}
			got, err := lookPathDirMode(`C:\work`, tt.env, tt.file, find, true)
			if err != nil {
				t.Fatalf("lookPathDirMode returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("wrong result: want %q, got %q", tt.want, got)
			}
			if !reflect.DeepEqual(gotTry, tt.wantTry) {
				t.Fatalf("wrong lookup attempts:\nwant: %#v\ngot:  %#v", tt.wantTry, gotTry)
			}
			if !reflect.DeepEqual(gotExts, tt.wantExts) {
				t.Fatalf("wrong PATHEXT list:\nwant: %#v\ngot:  %#v", tt.wantExts, gotExts)
			}
		})
	}
}

func trimWindowsExt(path string, exts []string) string {
	for _, ext := range exts {
		if len(path) >= len(ext) && path[len(path)-len(ext):] == ext {
			return path[:len(path)-len(ext)]
		}
	}
	return path
}

func TestFindExecutableWithWindowsExtensions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rpc-server.exe"), []byte("echo ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "rpc-server.exe"), []byte("echo ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		file string
		exts []string
		want string
	}{
		{
			name: "bare name resolves exe",
			file: "rpc-server",
			exts: []string{".exe"},
			want: "rpc-server.exe",
		},
		{
			name: "relative slash path resolves exe",
			file: "bin/rpc-server",
			exts: []string{".com", ".exe"},
			want: "bin/rpc-server.exe",
		},
		{
			name: "explicit exe returns as-is",
			file: "bin/rpc-server.exe",
			exts: []string{".exe"},
			want: "bin/rpc-server.exe",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := findExecutable(dir, tt.file, tt.exts)
			if err != nil {
				t.Fatalf("findExecutable returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("wrong result: want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestLookPathHasPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		file    string
		windows bool
		want    bool
	}{
		{"unix bare", "rpc-server", false, false},
		{"unix slash", "bin/rpc-server", false, true},
		{"unix backslash is name", `bin\rpc-server`, false, false},
		{"windows bare", "rpc-server", true, false},
		{"windows slash", "bin/rpc-server", true, true},
		{"windows backslash", `bin\rpc-server`, true, true},
		{"windows drive", `C:\bin\rpc-server`, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lookPathHasPath(tt.file, tt.windows)
			if got != tt.want {
				t.Fatalf("wrong result: want %v, got %v", tt.want, got)
			}
		})
	}
}
