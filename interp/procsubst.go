// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"io/fs"
	"os"
	"runtime"
	"strings"
	"time"
)

// procSubstPipe is the platform seam behind process substitution: a
// filesystem-visible rendezvous path whose other end the shell (or a child
// process) opens by name. On Unix it is a FIFO in the runner's temp dir; on
// Windows it is a \\.\pipe\ named pipe. Both open calls block until the
// consumer side opens the path, matching Bash's FIFO semantics.
type procSubstPipe interface {
	// path is the name substituted into the command line.
	path() string
	// openWriter opens the substitution's write end (`<(cmd)`).
	openWriter() (*os.File, error)
	// openReader opens the substitution's read end (`>(cmd)`).
	openReader() (*os.File, error)
	// cleanup releases the rendezvous once the substitution is done. It is
	// safe to call after the opened end has been closed.
	cleanup()
}

// Windows process-substitution pipes have two spellings. CreateNamedPipe is
// given the documented native form, \\.\pipe\sh-np-<hex>. The command line
// gets //./pipe/sh-np-<hex>: the same local-device path (Win32 path
// normalisation accepts either separator before the \\.\ check, and Go's
// os.OpenFile passes a short //./ path to CreateFile untouched), but one
// that survives being re-read by the shell — `eval cat <(echo x)` used to
// hand cat `\.pipesh-np-…` once the backslashes had served as escapes.
// pathconv.ToOSMountsMode passes a leading // through unchanged, so the
// mount table never prefixes it with a drive.
const (
	windowsProcSubstNativeDir = `\\.\pipe\`
	windowsProcSubstShellDir  = `//./pipe/`
)

// windowsProcSubstPipeNames returns the native (CreateNamedPipe) and shell
// (command-line) spellings of the pipe for one random suffix.
func windowsProcSubstPipeNames(suffix string) (native, shell string) {
	return windowsProcSubstNativeDir + fifoNamePrefix + suffix,
		windowsProcSubstShellDir + fifoNamePrefix + suffix
}

// isProcSubstPipePath reports whether path names one of this shell's
// Windows process-substitution pipes, in either spelling. Only meaningful
// in windows mode; every other host has its FIFOs in the temp dir.
func isProcSubstPipePath(path string) bool {
	return isProcSubstPipePathMode(path, runtime.GOOS == "windows")
}

// isProcSubstPipePathMode is [isProcSubstPipePath] with an explicit windows
// flag.
func isProcSubstPipePathMode(path string, windows bool) bool {
	if !windows {
		return false
	}
	for _, dir := range [...]string{windowsProcSubstShellDir, windowsProcSubstNativeDir} {
		if rest, ok := strings.CutPrefix(path, dir); ok {
			return strings.HasPrefix(rest, fifoNamePrefix) && !strings.ContainsAny(rest, `/\`)
		}
	}
	return false
}

// procSubstPipeInfo is the synthetic stat result for a Windows
// process-substitution pipe. A named pipe has no filesystem node: every
// CreateFile on \\.\pipe\<name> — os.Stat's included — connects a client to
// the single server instance, which would consume the rendezvous meant for
// the real consumer and make its open fail. The shell therefore answers
// its own `test -e`/`-p`/`[[ -e ]]` of the path without touching the pipe,
// the way stat() of a FIFO on Unix does not open it.
type procSubstPipeInfo struct{ name string }

func (i procSubstPipeInfo) Name() string     { return i.name }
func (procSubstPipeInfo) Size() int64        { return 0 }
func (procSubstPipeInfo) Mode() fs.FileMode  { return fs.ModeNamedPipe | 0o600 }
func (procSubstPipeInfo) ModTime() time.Time { return time.Time{} }
func (procSubstPipeInfo) IsDir() bool        { return false }
func (procSubstPipeInfo) Sys() any           { return nil }

// procSubstPipeStat reports the synthetic stat of a process-substitution
// pipe path when path names one.
func procSubstPipeStat(path string) (fs.FileInfo, bool) {
	return procSubstPipeStatMode(path, runtime.GOOS == "windows")
}

// procSubstPipeStatMode is [procSubstPipeStat] with an explicit windows flag.
func procSubstPipeStatMode(path string, windows bool) (fs.FileInfo, bool) {
	if !isProcSubstPipePathMode(path, windows) {
		return nil, false
	}
	name := path
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		name = path[i+1:]
	}
	return procSubstPipeInfo{name: name}, true
}
