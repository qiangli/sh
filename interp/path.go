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
	if windows {
		// dir is in the shell's spelling (/c/Users/… after a cd); joined as-is
		// it becomes \c\Users\…, the drive-relative path C:\c\Users\…, and a
		// relative executable or file after a cd is "not found". Resolve the
		// directory to its OS form first.
		dir = shellPathToOSMode(dir, dir, windows)
	}
	if !windows || runtime.GOOS == "windows" {
		return filepath.Join(dir, path)
	}
	if strings.HasSuffix(dir, `/`) || strings.HasSuffix(dir, `\`) {
		return dir + path
	}
	return dir + `\` + path
}

// ShellPathToOS converts a path in the shell's own spelling into the host's
// native form, resolved against dir when relative: on Windows the MSYS drive
// form (/c/…), a drive-relative /foo and C:\… all become real drive paths; on
// every other host the path is returned unchanged. Embedders that open a
// script operand themselves (bashy's argv[1], `bashy -c` callers) use it so
// `bashy "$HOME/x.sh"` works on Windows, where $HOME is /c/Users/….
func ShellPathToOS(dir, path string) string { return shellPathToOS(dir, path) }

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

// msysDrivePath recognizes the MSYS/Git-Bash drive convention "/c" or "/c/...".
// It also accepts the backslash form "\c\..." which Go can produce on Windows
// when globbing joins an MSYS-style PWD with a relative pattern. It returns the
// UPPERCASE drive letter and the remainder beginning with "/" ("/c" -> "C","/";
// "/c/Users" -> "C","/Users"). A bare "/foo" is left to the volume-prepend
// fallback (it is not a drive reference).
func msysDrivePath(path string) (drive, rest string, ok bool) {
	if len(path) >= 2 && isWindowsSlash(path[0]) && isWindowsDriveLetter(path[1]) &&
		(len(path) == 2 || isWindowsSlash(path[2])) {
		r := path[2:]
		if r == "" {
			r = "/"
		}
		r = strings.ReplaceAll(r, `\`, "/")
		return string(path[1] &^ 0x20), r, true // &^0x20 = ASCII upper
	}
	return "", "", false
}

// windowsPathEnvNames are the variables a native Windows child reads as a
// filesystem path. bashy hands scripts these in the MSYS drive spelling
// (/c/Users/…) so scripts stay portable, but CreateFile has no idea what /c
// is: go.exe dies with "creating work dir … D:\c\Users\…" when the current
// drive is D:, and rustc's temp dir the same way. Matched case-insensitively,
// as Windows environment names are.
var windowsPathEnvNames = map[string]bool{
	"TEMP": true, "TMP": true, "TMPDIR": true,
	"HOME": true, "USERPROFILE": true,
	"GOPATH": true, "GOCACHE": true, "GOMODCACHE": true, "GOROOT": true,
	"CARGO_HOME": true, "RUSTUP_HOME": true,
	"LOCALAPPDATA": true, "APPDATA": true, "PROGRAMDATA": true,
	"SYSTEMROOT": true, "WINDIR": true,
}

// nativeExecEnv rewrites the path-valued variables of a child's environment
// into the host's native spelling on Windows: an absolute MSYS drive path
// (/c/Users/x) becomes C:\Users\x, and each such element of PATH is converted
// in place. Every other value — a native path, a relative one, a bare /foo,
// the empty string — stays byte-identical, and the shell's own variables are
// untouched, so scripts keep seeing the MSYS form. On every other host env is
// returned as is.
func nativeExecEnv(env []string) []string {
	return nativeExecEnvMode(env, runtime.GOOS == "windows")
}

func nativeExecEnvMode(env []string, windows bool) []string {
	if !windows {
		return env
	}
	var out []string
	for i, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		var conv string
		switch {
		case strings.EqualFold(name, "PATH"):
			conv = nativeExecPathList(value)
		case windowsPathEnvNames[strings.ToUpper(name)]:
			conv = nativeExecPath(value)
		default:
			continue
		}
		if conv == value {
			continue
		}
		if out == nil {
			out = append([]string(nil), env...)
		}
		out[i] = name + "=" + conv
	}
	if out == nil {
		return env
	}
	return out
}

// nativeExecPath converts one absolute MSYS drive path (/c or /c/…); anything
// else is returned unchanged. Only the forward-slash form counts: a value
// holding a colon is never a single MSYS path, and \c\… is a drive-relative
// native path a child can already open.
func nativeExecPath(value string) string {
	if len(value) < 2 || value[0] != '/' || strings.Contains(value, ":") {
		return value
	}
	drive, rest, ok := msysDrivePath(value)
	if !ok {
		return value
	}
	return drive + ":" + strings.ReplaceAll(rest, "/", `\`)
}

// nativeExecPathList converts each MSYS-form element of a ;-separated PATH,
// keeping the other elements and the separators as they are.
func nativeExecPathList(value string) string {
	if !strings.Contains(value, "/") {
		return value
	}
	elems := strings.Split(value, ";")
	for i, e := range elems {
		elems[i] = nativeExecPath(e)
	}
	return strings.Join(elems, ";")
}
