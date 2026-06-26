// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"path/filepath"
	"runtime"
	"strings"
)

func shellPathAbs(path string) bool {
	return shellPathAbsMode(path, runtime.GOOS == "windows")
}

func shellPathAbsMode(path string, windows bool) bool {
	if !windows {
		return filepath.IsAbs(path)
	}
	if runtime.GOOS == "windows" && filepath.IsAbs(path) {
		return true
	}
	if len(path) >= 3 && isWindowsDriveLetter(path[0]) && path[1] == ':' && isWindowsSlash(path[2]) {
		return true
	}
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`)
}

func shellPathJoinAbs(dir, path string) string {
	return shellPathJoinAbsMode(dir, path, runtime.GOOS == "windows")
}

func shellPathJoinAbsMode(dir, path string, windows bool) string {
	if path == "" || shellPathAbsMode(path, windows) {
		return shellPathToOSMode(dir, path, windows)
	}
	if !windows || runtime.GOOS == "windows" {
		return filepath.Join(dir, path)
	}
	if strings.HasSuffix(dir, `/`) || strings.HasSuffix(dir, `\`) {
		return dir + path
	}
	return dir + `\` + path
}

func shellPathToOS(dir, path string) string {
	return shellPathToOSMode(dir, path, runtime.GOOS == "windows")
}

func shellPathToOSMode(dir, path string, windows bool) string {
	if !windows || path == "" {
		return path
	}
	if len(path) >= 2 && isWindowsDriveLetter(path[0]) && path[1] == ':' {
		return windowsClean(path)
	}
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, `\`) {
		return path
	}
	vol := windowsVolumeName(dir)
	if vol == "" {
		vol = "C:"
	}
	return windowsClean(vol + path)
}

func windowsClean(path string) string {
	if runtime.GOOS == "windows" {
		return filepath.Clean(filepath.FromSlash(path))
	}
	return strings.ReplaceAll(path, "/", `\`)
}

func windowsVolumeName(path string) string {
	if runtime.GOOS == "windows" {
		return filepath.VolumeName(path)
	}
	if len(path) >= 2 && isWindowsDriveLetter(path[0]) && path[1] == ':' {
		return path[:2]
	}
	return ""
}

func isWindowsDriveLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isWindowsSlash(c byte) bool {
	return c == '/' || c == '\\'
}
