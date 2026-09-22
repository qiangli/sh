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

// ToOSMode is [ToOS] with an explicit windows mode. It consults
// [CurrentMounts], so on a non-Windows host windows mode applies the plain
// drive rules.
func ToOSMode(dir, path string, windows bool) string {
	return ToOSMountsMode(CurrentMounts(), dir, path, windows)
}

// ToOSMountsMode is [ToOSMode] with an explicit mount table, so a virtual
// root can be exercised on any host. Precedence: device/UNC passthrough,
// native X:\…, /dev/null, the longest explicit mount other than "/", the
// drive forms (/c, /mnt/c), the "/" mount, and finally the drive-relative
// fallback that prepends dir's volume. Characters NTFS refuses in a
// filename are encoded per [EncodeSpecialMode] on the way out.
func ToOSMountsMode(m *Mounts, dir, path string, windows bool) string {
	if !windows || path == "" {
		return path
	}
	// Device and UNC-prefixed paths (\\.\pipe\x, //server/share) are already
	// native and must not gain a drive prefix. Full UNC handling is out of
	// scope; this is only a passthrough so such paths aren't corrupted.
	if len(path) >= 2 && isSlash(path[0]) && isSlash(path[1]) {
		return path
	}
	if isNativeDrivePath(path) {
		return clean(EncodeSpecialMode(path, true))
	}
	if path == "/dev/null" {
		return "NUL"
	}
	if native, rest, ok := m.lookupPosix(path, false); ok {
		return clean(mountNative(native, rest))
	}
	if p, ok := normalizeOperand(path); ok {
		return p
	}
	if drive, rest, ok := DrivePath(path); ok {
		return clean(string(drive) + ":" + EncodeSpecialMode(rest, true))
	}
	if native, rest, ok := m.lookupPosix(path, true); ok {
		return clean(mountNative(native, rest))
	}
	if !strings.HasPrefix(path, "/") && !strings.HasPrefix(path, `\`) {
		return EncodeSpecialMode(path, true)
	}
	vol := volumeName(dir)
	if vol == "" {
		vol = "C:"
	}
	return clean(vol + EncodeSpecialMode(path, true))
}

// isNativeDrivePath reports whether path is spelled with a drive prefix:
// "C:", "C:\x" or "C:/x". "a:b" is not one — the colon is a character in a
// filename, which [EncodeSpecialMode] maps for NTFS.
func isNativeDrivePath(path string) bool {
	return len(path) >= 2 && isDriveLetter(path[0]) && path[1] == ':' &&
		(len(path) == 2 || isSlash(path[2]))
}

// FromOS converts a native path into the shell's spelling: on Windows
// C:\Users\x becomes /c/Users/x; on every other host the path is returned
// unchanged.
func FromOS(path string) string {
	return FromOSMode(path, runtime.GOOS == "windows")
}

// FromOSMode is [FromOS] with an explicit windows mode; it consults
// [CurrentMounts].
func FromOSMode(path string, windows bool) string {
	return FromOSMountsMode(CurrentMounts(), path, windows)
}

// FromOSMountsMode is [FromOSMode] with an explicit mount table: the
// longest mount whose native directory prefixes path wins, then the drive
// rule (C:\Users\x -> /c/Users/x, C:\ -> /c). Characters encoded by
// [EncodeSpecialMode] are decoded.
func FromOSMountsMode(m *Mounts, path string, windows bool) string {
	if !windows || path == "" {
		return path
	}
	if posix, ok := m.FromOS(path); ok {
		return DecodeSpecialMode(posix, true)
	}
	p := strings.ReplaceAll(path, `\`, "/")
	if len(p) >= 2 && isDriveLetter(p[0]) && p[1] == ':' {
		rest := strings.TrimRight(p[2:], "/")
		if rest != "" && rest[0] != '/' {
			rest = "/" + rest
		}
		drive := p[0]
		if 'A' <= drive && drive <= 'Z' {
			drive += 'a' - 'A'
		}
		return DecodeSpecialMode("/"+string(drive)+rest, true)
	}
	return DecodeSpecialMode(p, true)
}

// specialBase is the private-use code point Cygwin and MSYS add to a
// character NTFS refuses in a filename, so a mixed userland agrees on the
// on-disk spelling of `a:b`.
const specialBase = 0xF000

// EncodeSpecialMode maps the characters NTFS forbids in a filename
// (: * ? " < > |) inside path components to U+F000+char, the Cygwin/MSYS
// convention. A drive colon ("C:", "C:\x") and a device/UNC-prefixed path
// (\\.\x, //server/share) are left alone. Off Windows the path is returned
// unchanged.
func EncodeSpecialMode(path string, windows bool) string {
	if !windows || path == "" {
		return path
	}
	if len(path) >= 2 && isSlash(path[0]) && isSlash(path[1]) {
		return path
	}
	if !strings.ContainsAny(path, `:*?"<>|`) {
		return path
	}
	start := 0
	if isNativeDrivePath(path) {
		start = 2
	}
	var b strings.Builder
	b.Grow(len(path) + 8)
	b.WriteString(path[:start])
	for _, r := range path[start:] {
		switch r {
		case ':', '*', '?', '"', '<', '>', '|':
			b.WriteRune(specialBase + r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DecodeSpecialMode reverses [EncodeSpecialMode]: every rune in
// U+F000..U+F0FF becomes the character it stands for. Off Windows the path
// is returned unchanged.
func DecodeSpecialMode(path string, windows bool) string {
	if !windows || path == "" {
		return path
	}
	if !strings.ContainsFunc(path, isSpecialRune) {
		return path
	}
	var b strings.Builder
	b.Grow(len(path))
	for _, r := range path {
		if isSpecialRune(r) {
			b.WriteByte(byte(r - specialBase))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isSpecialRune(r rune) bool {
	return r >= specialBase && r <= specialBase+0xFF
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
	if windows {
		// Encode before joining: filepath.Clean must never see a colon that
		// is not the drive's.
		path = EncodeSpecialMode(path, true)
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
// volume-prepend fallback in [ToOS] (it is not a drive reference), and so
// is a single-letter first component whose drive is not present per
// [LogicalDrives]: /a/b/c on a host without an A: drive is a POSIX path.
func DrivePath(path string) (drive byte, rest string, ok bool) {
	return DrivePathIn(LogicalDrives(), path)
}

// DrivePathIn is [DrivePath] against an explicit set of present drives.
func DrivePathIn(drives DriveSet, path string) (drive byte, rest string, ok bool) {
	// WSL form: /mnt/c[/...]. Only the forward-slash spelling exists in the
	// wild; require it exactly.
	if len(path) >= 6 && strings.HasPrefix(path, "/mnt/") &&
		isDriveLetter(path[5]) && (len(path) == 6 || isSlash(path[6])) {
		if !drives.Has(path[5]) {
			return 0, "", false
		}
		r := path[6:]
		if r == "" {
			r = "/"
		}
		r = strings.ReplaceAll(r, `\`, "/")
		return path[5] &^ 0x20, r, true // &^0x20 = ASCII upper
	}
	if len(path) >= 2 && isSlash(path[0]) && isDriveLetter(path[1]) &&
		(len(path) == 2 || isSlash(path[2])) {
		if !drives.Has(path[1]) {
			return 0, "", false
		}
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

// normalizeOperand maps /tmp[/...] to the host temp directory when no
// mount table says otherwise. Only the forward-slash POSIX spelling counts —
// `\tmp\x` is a drive-relative native path. Anything else is left to the
// regular conversions.
func normalizeOperand(path string) (string, bool) {
	if strings.HasPrefix(path, "/tmp") && (len(path) == 4 || path[4] == '/') {
		rest := path[4:]
		if rest == "" {
			return clean(TempDir()), true
		}
		return clean(TempDir() + EncodeSpecialMode(rest, true)), true
	}
	return "", false
}

// NativePath converts one absolute MSYS or WSL drive path (/c/…, /mnt/c/…)
// to its native backslash form; anything else is returned unchanged. Only
// the forward-slash form counts: a value holding a colon is never a single
// MSYS path, and \c\… is a drive-relative native path a child can already
// open. The drive must be present per [LogicalDrives]; /a/b/c stays as it
// is on a host without an A: drive. See [NativePathMounts] for the
// mount-aware form.
func NativePath(value string) string {
	return NativePathMounts(nil, value)
}

// NativePathList converts a path list into native form: each MSYS/WSL-form
// element is converted via [NativePath], and a `:`-separated shell-form
// list becomes `;`-separated. A list that already uses `;` keeps its
// separators, and a native drive path cut by the `:` split ("C:\x") is
// re-joined, so a value of native paths is returned unchanged. See
// [NativePathListMounts] for the mount-aware form.
func NativePathList(value string) string {
	return NativePathListMounts(nil, value)
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
