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
	// MSYS/Git-Bash drive convention: /c or /c/... -> C:\... — the way every
	// Windows dev tool spells a drive as a POSIX path, so scripts stay portable.
	if drive, rest, ok := msysDrivePath(path); ok {
		return windowsClean(drive + ":" + rest)
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

func shellPathFromOS(path string) string {
	return shellPathFromOSMode(path, runtime.GOOS == "windows")
}

func shellPathFromOSMode(path string, windows bool) string {
	if !windows || path == "" {
		return path
	}
	p := strings.ReplaceAll(path, `\`, "/")
	if len(p) >= 2 && isWindowsDriveLetter(p[0]) && p[1] == ':' {
		rest := p[2:]
		if rest == "" {
			rest = "/"
		} else if rest[0] != '/' {
			rest = "/" + rest
		}
		drive := p[0]
		if 'A' <= drive && drive <= 'Z' {
			drive += 'a' - 'A'
		}
		return "/" + string(drive) + rest
	}
	if strings.HasPrefix(p, "//") {
		return p
	}
	if strings.HasPrefix(p, "/") {
		return p
	}
	return p
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

// msysDrivePath recognizes the MSYS/Git-Bash drive convention "/c" or "/c/...":
// a leading forward slash, a drive letter, then end-of-string or a slash. It
// returns the UPPERCASE drive letter and the remainder beginning with "/"
// ("/c" -> "C","/"; "/c/Users" -> "C","/Users"). A bare "/foo" is left to the
// volume-prepend fallback (it is not a drive reference).
func msysDrivePath(path string) (drive, rest string, ok bool) {
	if len(path) >= 2 && path[0] == '/' && isWindowsDriveLetter(path[1]) &&
		(len(path) == 2 || isWindowsSlash(path[2])) {
		r := path[2:]
		if r == "" {
			r = "/"
		}
		return string(path[1] &^ 0x20), r, true // &^0x20 = ASCII upper
	}
	return "", "", false
}
