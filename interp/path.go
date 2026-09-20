// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/pathconv"
)

// The conversion core lives in the reusable mvdan.cc/sh/v3/pathconv package;
// these wrappers keep the interpreter's historical names so the many call
// sites (cd/PWD, open, stat, glob, exec) stay unchanged.

func shellPathAbs(path string) bool {
	return pathconv.IsAbs(path)
}

func shellPathAbsMode(path string, windows bool) bool {
	return pathconv.IsAbsMode(path, windows)
}

func shellPathJoinAbs(dir, path string) string {
	return pathconv.JoinAbs(dir, path)
}

func shellPathJoinAbsMode(dir, path string, windows bool) string {
	return pathconv.JoinAbsMode(dir, path, windows)
}

// ShellPathToOS converts a path in the shell's own spelling into the host's
// native form, resolved against dir when relative: on Windows the MSYS drive
// form (/c/…), the WSL form (/mnt/c/…), a drive-relative /foo and C:\… all
// become real drive paths; on every other host the path is returned
// unchanged. Embedders that open a script operand themselves (bashy's
// argv[1], `bashy -c` callers) use it so `bashy "$HOME/x.sh"` works on
// Windows, where $HOME is /c/Users/…. See [pathconv.ToOS].
func ShellPathToOS(dir, path string) string { return shellPathToOS(dir, path) }

func shellPathToOS(dir, path string) string {
	return pathconv.ToOS(dir, path)
}

func shellPathToOSMode(dir, path string, windows bool) string {
	return pathconv.ToOSMode(dir, path, windows)
}

func shellPathFromOS(path string) string {
	return pathconv.FromOS(path)
}

func shellPathFromOSMode(path string, windows bool) string {
	return pathconv.FromOSMode(path, windows)
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

// parseBashyEnv parses the BASHYENV variable, a WSLENV-style opt-in list of
// variables to path-convert at the child-process boundary:
//
//	BASHYENV=VAR/p:VAR2/l
//
// /p converts the value as a single path, /l as a path list (a :-separated
// shell-form list becomes ;-separated native). Names are matched
// case-insensitively; an entry without a supported flag is ignored.
func parseBashyEnv(spec string) map[string]byte {
	var m map[string]byte
	for entry := range strings.SplitSeq(spec, ":") {
		name, flags, ok := strings.Cut(entry, "/")
		if !ok || name == "" {
			continue
		}
		var flag byte
		switch {
		case strings.ContainsRune(flags, 'l'):
			flag = 'l'
		case strings.ContainsRune(flags, 'p'):
			flag = 'p'
		default:
			continue
		}
		if m == nil {
			m = make(map[string]byte)
		}
		m[strings.ToUpper(name)] = flag
	}
	return m
}

// nativeExecEnv rewrites the path-valued variables of a child's environment
// into the host's native spelling on Windows: an absolute MSYS drive path
// (/c/Users/x) becomes C:\Users\x, and each such element of PATH is converted
// in place. Variables named in BASHYENV (VAR/p:VAR2/l) are converted per
// their flag, overriding the built-in name list. Every other value — a
// native path, a relative one, a bare /foo, the empty string — stays
// byte-identical, and the shell's own variables are untouched, so scripts
// keep seeing the MSYS form. On every other host env is returned as is.
func nativeExecEnv(env []string) []string {
	return nativeExecEnvMode(env, runtime.GOOS == "windows")
}

func nativeExecEnvMode(env []string, windows bool) []string {
	if !windows {
		return env
	}
	var bashyEnv map[string]byte
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok && strings.EqualFold(name, "BASHYENV") {
			bashyEnv = parseBashyEnv(value)
			break
		}
	}
	var out []string
	for i, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		var conv string
		if flag, listed := bashyEnv[strings.ToUpper(name)]; listed {
			if flag == 'l' {
				conv = pathconv.NativePathList(value)
			} else {
				conv = pathconv.NativePath(value)
			}
		} else {
			switch {
			case strings.EqualFold(name, "PATH"):
				conv = nativeExecPathList(value)
			case windowsPathEnvNames[strings.ToUpper(name)]:
				conv = pathconv.NativePath(value)
			default:
				continue
			}
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

// nativeExecPathList converts each MSYS-form element of a ;-separated PATH,
// keeping the other elements and the separators as they are. Unlike
// [pathconv.NativePathList] it never re-splits on ':' — the shell's PATH is
// already ;-separated on Windows, and a stray colon must not mangle it.
func nativeExecPathList(value string) string {
	if !strings.Contains(value, "/") {
		return value
	}
	elems := strings.Split(value, ";")
	for i, e := range elems {
		elems[i] = pathconv.NativePath(e)
	}
	return strings.Join(elems, ";")
}
