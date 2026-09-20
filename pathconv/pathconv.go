// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// Package pathconv converts between the shell's portable POSIX spelling of
// filesystem paths and the host's native Windows spelling.
//
// It accepts every drive spelling a script is likely to hand a Windows
// shell: native backslash (`C:\Users\x`), native forward slash
// (`C:/Users/x`), the MSYS/Git-Bash drive form (`/c`, `/c/Users/x`), and
// the WSL mount form (`/mnt/c`, `/mnt/c/Users/x`). Device and UNC-prefixed
// paths (`\\.\pipe\x`, `//server/share`) pass through untouched.
//
// Every conversion has a *Mode variant taking an explicit windows flag so
// the Windows behavior is testable on any host; the plain functions apply
// the host's runtime.GOOS. On non-Windows hosts all conversions are the
// identity.
package pathconv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// TempDir is the directory that the shell-boundary operand normalization
// maps /tmp to on Windows. It is a variable so tests can pin it; everything
// else should leave it as os.TempDir.
var TempDir = os.TempDir

// IsAbs reports whether the path is absolute in the shell's spelling.
func IsAbs(path string) bool {
	return IsAbsMode(path, runtime.GOOS == "windows")
}

// IsAbsMode is [IsAbs] with an explicit windows mode.
func IsAbsMode(path string, windows bool) bool {
	if !windows {
		return filepath.IsAbs(path)
	}
	if runtime.GOOS == "windows" && filepath.IsAbs(path) {
		return true
	}
	if len(path) >= 3 && isDriveLetter(path[0]) && path[1] == ':' && isSlash(path[2]) {
		return true
	}
	return strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`)
}

// ToOS converts a path in the shell's own spelling into the host's native
// form: on Windows the MSYS drive form (/c/…), the WSL form (/mnt/c/…), a
// drive-relative /foo and C:\… all become real drive paths, /dev/null
// becomes NUL and /tmp becomes the host temp directory; on every other host
// the path is returned unchanged. dir supplies the drive for a
// drive-relative /foo.
func ToOS(dir, path string) string {
	return ToOSMode(dir, path, runtime.GOOS == "windows")
}

// ToOSMode is [ToOS] with an explicit windows mode.
func ToOSMode(dir, path string, windows bool) string {
	if !windows || path == "" {
		return path
	}
	// Device and UNC-prefixed paths (\\.\pipe\x, //server/share) are already
	// native and must not gain a drive prefix. Full UNC handling is out of
	// scope; this is only a passthrough so such paths aren't corrupted.
	if len(path) >= 2 && isSlash(path[0]) && isSlash(path[1]) {
		return path
	}
	if p, ok := normalizeOperand(path); ok {
		return p
	}
	if len(path) >= 2 && isDriveLetter(path[0]) && path[1] == ':' {
		return clean(path)
	}
	if drive, rest, ok := DrivePath(path); ok {
		return clean(string(drive) + ":" + rest)
	}
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, `\`) {
		return path
	}
	vol := volumeName(dir)
	if vol == "" {
		vol = "C:"
	}
	return clean(vol + path)
}

// FromOS converts a native path into the shell's spelling: on Windows
// C:\Users\x becomes /c/Users/x; on every other host the path is returned
// unchanged.
func FromOS(path string) string {
	return FromOSMode(path, runtime.GOOS == "windows")
}

// FromOSMode is [FromOS] with an explicit windows mode.
func FromOSMode(path string, windows bool) string {
	if !windows || path == "" {
		return path
	}
	p := strings.ReplaceAll(path, `\`, "/")
	if len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':' {
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
	return p
}

// JoinAbs resolves path against dir and converts the result to the host's
// native form when path is absolute in the shell's spelling; a relative path
// is joined onto dir.
func JoinAbs(dir, path string) string {
	return JoinAbsMode(dir, path, runtime.GOOS == "windows")
}

// JoinAbsMode is [JoinAbs] with an explicit windows mode.
func JoinAbsMode(dir, path string, windows bool) string {
	if path == "" || IsAbsMode(path, windows) {
		return ToOSMode(dir, path, windows)
	}
	if windows {
		// dir is in the shell's spelling (/c/Users/… after a cd); joined as-is
		// it becomes \c\Users\…, the drive-relative path C:\c\Users\…, and a
		// relative executable or file after a cd is "not found". Resolve the
		// directory to its OS form first.
		dir = ToOSMode(dir, dir, windows)
	}
	if !windows || runtime.GOOS == "windows" {
		return filepath.Join(dir, path)
	}
	if strings.HasSuffix(dir, `/`) || strings.HasSuffix(dir, `\`) {
		return dir + path
	}
	return dir + `\` + path
}

// ToSlash converts a shell-spelling or native path to the native Windows
// drive form with forward slashes — the spelling Git-Bash's `pwd -W`
// prints (`C:/Users/x`). dir supplies the drive for drive-relative paths.
func ToSlash(dir, path string) string {
	return ToSlashMode(dir, path, runtime.GOOS == "windows")
}

// ToSlashMode is [ToSlash] with an explicit windows mode.
func ToSlashMode(dir, path string, windows bool) string {
	if !windows {
		return path
	}
	return strings.ReplaceAll(ToOSMode(dir, path, windows), `\`, "/")
}

// DrivePath recognizes the MSYS/Git-Bash drive convention "/c" or "/c/..."
// and the WSL mount convention "/mnt/c" or "/mnt/c/...". It also accepts the
// backslash form "\c\..." which Go can produce on Windows when globbing
// joins an MSYS-style PWD with a relative pattern. It returns the UPPERCASE
// drive letter and the remainder beginning with "/" ("/c" -> 'C',"/";
// "/mnt/c/Users" -> 'C',"/Users"). A bare "/foo" is left to the
// volume-prepend fallback in [ToOS] (it is not a drive reference).
func DrivePath(path string) (drive byte, rest string, ok bool) {
	// WSL form: /mnt/c[/...]. Only the forward-slash spelling exists in the
	// wild; require it exactly.
	if len(path) >= 6 && strings.HasPrefix(path, "/mnt/") &&
		isDriveLetter(path[5]) && (len(path) == 6 || isSlash(path[6])) {
		r := path[6:]
		if r == "" {
			r = "/"
		}
		r = strings.ReplaceAll(r, `\`, "/")
		return path[5] &^ 0x20, r, true // &^0x20 = ASCII upper
	}
	if len(path) >= 2 && isSlash(path[0]) && isDriveLetter(path[1]) &&
		(len(path) == 2 || isSlash(path[2])) {
		r := path[2:]
		if r == "" {
			r = "/"
		}
		r = strings.ReplaceAll(r, `\`, "/")
		return path[1] &^ 0x20, r, true
	}
	return 0, "", false
}

// DriveOf returns the UPPERCASE drive letter of a native drive path
// ("C:\x", "c:/x"), or ok=false when the path has no drive prefix.
func DriveOf(path string) (drive byte, ok bool) {
	if len(path) >= 2 && isDriveLetter(path[0]) && path[1] == ':' {
		return path[0] &^ 0x20, true
	}
	return 0, false
}

// normalizeOperand maps the two POSIX pseudo-operands every script uses in
// its first hour to their Windows equivalents: /dev/null -> NUL and
// /tmp[/...] -> the host temp directory. Only the forward-slash POSIX
// spelling counts — `\tmp\x` is a drive-relative native path. Anything else
// is left to the regular conversions.
func normalizeOperand(path string) (string, bool) {
	if path == "/dev/null" {
		return "NUL", true
	}
	if strings.HasPrefix(path, "/tmp") && (len(path) == 4 || path[4] == '/') {
		rest := path[4:]
		if rest == "" {
			return clean(TempDir()), true
		}
		return clean(TempDir() + rest), true
	}
	return "", false
}

// NativePath converts one absolute MSYS or WSL drive path (/c/…, /mnt/c/…)
// to its native backslash form; anything else is returned unchanged. Only
// the forward-slash form counts: a value holding a colon is never a single
// MSYS path, and \c\… is a drive-relative native path a child can already
// open.
func NativePath(value string) string {
	if len(value) < 2 || value[0] != '/' || strings.Contains(value, ":") {
		return value
	}
	drive, rest, ok := DrivePath(value)
	if !ok {
		return value
	}
	return string(drive) + ":" + strings.ReplaceAll(rest, "/", `\`)
}

// NativePathList converts a path list into native form: each MSYS/WSL-form
// element is converted via [NativePath], and a `:`-separated shell-form
// list becomes `;`-separated. A list that already uses `;` keeps its
// separators, and a value containing native drive paths (whose `:` would be
// mis-split) is returned unchanged.
func NativePathList(value string) string {
	if strings.Contains(value, ";") {
		elems := strings.Split(value, ";")
		for i, e := range elems {
			elems[i] = NativePath(e)
		}
		return strings.Join(elems, ";")
	}
	if !strings.Contains(value, ":") {
		return NativePath(value)
	}
	elems := strings.Split(value, ":")
	for _, e := range elems {
		// A single-letter element means the split cut a native drive path
		// ("C:\x" -> "C", "\x"); the value is already native, leave it.
		if len(e) == 1 && isDriveLetter(e[0]) {
			return value
		}
	}
	for i, e := range elems {
		elems[i] = NativePath(e)
	}
	return strings.Join(elems, ";")
}

func clean(path string) string {
	if runtime.GOOS == "windows" {
		return filepath.Clean(filepath.FromSlash(path))
	}
	return strings.ReplaceAll(path, "/", `\`)
}

func volumeName(path string) string {
	if runtime.GOOS == "windows" {
		return filepath.VolumeName(path)
	}
	if len(path) >= 2 && isDriveLetter(path[0]) && path[1] == ':' {
		return path[:2]
	}
	return ""
}

func isDriveLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isSlash(c byte) bool {
	return c == '/' || c == '\\'
}
