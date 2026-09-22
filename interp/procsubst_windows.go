// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build windows

package interp

import (
	"fmt"
	mathrand "math/rand/v2"
	"os"
	"strconv"

	"golang.org/x/sys/windows"
)

// procSubstNamedPipe backs process substitution on Windows with a
// \\.\pipe\ named pipe: the shell holds the server end, and the consumer —
// an external command or the shell's own redirection — opens the substituted
// path with a plain CreateFile, which os.OpenFile already performs. This is
// the smallest seam that makes `<(cmd)` and `>(cmd)` work without fd
// inheritance or /dev/fd emulation.
//
// The pipe is a single instance (FILE_FLAG_FIRST_PIPE_INSTANCE, max 1), and
// unlike a FIFO every CreateFile on its name is a connection, so the path
// can be opened exactly once, by the consumer the substitution was written
// for (cat, diff <(a) <(b), `done < <(cmd)`, exec N< <(cmd)). A second
// opener gets ERROR_PIPE_BUSY ("All pipe instances are busy") while the
// first client is connected, and ERROR_FILE_NOT_FOUND once the server end
// has closed and the name is gone. A stat by a separate process (`ls -l`
// on the path) is such a second opener; the shell's own stat/test of the
// path is answered synthetically instead (see procSubstPipeStat).
type procSubstNamedPipe struct {
	// shellPath is the //./pipe/ spelling substituted into the command
	// line; the handle was created from the native \\.\pipe\ spelling of
	// the same name (windowsProcSubstPipeNames).
	shellPath string
	handle    windows.Handle
	opened    bool
}

func (r *Runner) newProcSubstPipe(substWrites bool) (procSubstPipe, error) {
	// The server end's direction is fixed at creation: for `<(cmd)` the
	// substitution writes and the consumer reads, for `>(cmd)` the reverse.
	openMode := uint32(windows.PIPE_ACCESS_INBOUND)
	if substWrites {
		openMode = windows.PIPE_ACCESS_OUTBOUND
	}
	for try := 0; ; try++ {
		native, shell := windowsProcSubstPipeNames(strconv.FormatUint(mathrand.Uint64(), 16))
		name16, err := windows.UTF16PtrFromString(native)
		if err != nil {
			return nil, fmt.Errorf("cannot create named pipe: %v", err)
		}
		h, err := windows.CreateNamedPipe(name16,
			openMode|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
			windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
			1, 4096, 4096, 0, nil)
		if err == nil {
			return &procSubstNamedPipe{shellPath: shell, handle: h}, nil
		}
		// FILE_FLAG_FIRST_PIPE_INSTANCE reports a name collision as
		// ERROR_ACCESS_DENIED; pick another random name.
		if err != windows.ERROR_ACCESS_DENIED && err != windows.ERROR_PIPE_BUSY {
			return nil, fmt.Errorf("cannot create named pipe: %v", err)
		}
		if try > 100 {
			return nil, fmt.Errorf("giving up at creating named pipe: %v", err)
		}
	}
}

func (p *procSubstNamedPipe) path() string { return p.shellPath }

// connect blocks until the consumer opens the substituted path, like the
// blocking FIFO open on Unix, then hands the server end over as an *os.File
// (whose Close also releases the handle).
func (p *procSubstNamedPipe) connect() (*os.File, error) {
	err := windows.ConnectNamedPipe(p.handle, nil)
	if err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return nil, err
	}
	p.opened = true
	return os.NewFile(uintptr(p.handle), p.shellPath), nil
}

func (p *procSubstNamedPipe) openWriter() (*os.File, error) { return p.connect() }

func (p *procSubstNamedPipe) openReader() (*os.File, error) { return p.connect() }

func (p *procSubstNamedPipe) cleanup() {
	// The named pipe object disappears with its last handle. When connect
	// handed the handle to an *os.File, that file's Close owns it.
	if !p.opened {
		windows.CloseHandle(p.handle)
	}
}
