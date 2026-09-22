// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build windows

package interp

import (
	"fmt"
	mathrand "math/rand/v2"
	"os"
	"strconv"
	"sync"

	"golang.org/x/sys/windows"
)

// procSubstNamedPipe backs process substitution on Windows with a
// \\.\pipe\ named pipe: the shell holds the server end, and the consumer —
// an external command or the shell's own redirection — opens the substituted
// path with a plain CreateFile, which os.OpenFile already performs. This is
// the smallest seam that makes `<(cmd)` and `>(cmd)` work without fd
// inheritance or /dev/fd emulation.
//
// Unlike a FIFO, every CreateFile on a pipe's name is a *connection* rather
// than a second handle on one file. Run #125 created a single instance, so
// the path could be opened exactly once and a consumer that opens it twice
// — our Go `diff` does, which is how `diff <(t1) <(t2)` reaches it — failed
// with ERROR_PIPE_BUSY, "All pipe instances are busy".
//
// For `<(cmd)`, where the consumer reads, the pipe is therefore created with
// PIPE_UNLIMITED_INSTANCES and the shell serves every connection from one
// copy of the substitution's output (see [procSubstBody]):
//
//   - The substitution is still started only once the path is opened for
//     the first time, the way a blocking FIFO open pairs the two sides.
//   - A second reader — sequential or concurrent — is replayed the body
//     from its first byte and then follows the live stream to EOF, so it
//     observes exactly what the first reader observes. That holds while the
//     body fits [procSubstReplayCap]; past that the oldest bytes are
//     dropped as the live readers move on and a late reader sees only the
//     bytes still held.
//   - A reader that opens the path after the shell released the rendezvous
//     (the substitution finished and cleanup ran) gets ERROR_FILE_NOT_FOUND,
//     as it did before: the name is gone.
//
// `>(cmd)`, where the shell reads and the consumer writes, keeps a single
// instance: there is one stdin to feed, and a second writer would have
// nowhere to go. A second opener of such a path still gets ERROR_PIPE_BUSY.
//
// A stat by a separate process (`ls -l` on the path) is also an opener; the
// shell's own stat/test of the path is answered synthetically instead (see
// procSubstPipeStat).
type procSubstNamedPipe struct {
	// shellPath is the //./pipe/ spelling substituted into the command
	// line; native is the \\.\pipe\ spelling CreateNamedPipe and
	// CreateFile are given (windowsProcSubstPipeNames).
	shellPath string
	native    string
	openMode  uint32
	maxInst   uint32

	// handle is the first server instance, until connect hands it over.
	handle   windows.Handle
	handedTo bool

	// body and the accept loop exist only for the multi-instance `<(cmd)`
	// direction.
	body     *procSubstBody
	stop     chan struct{}
	stopOnce sync.Once
}

func (r *Runner) newProcSubstPipe(substWrites bool) (procSubstPipe, error) {
	// The server end's direction is fixed at creation: for `<(cmd)` the
	// substitution writes and the consumer reads, for `>(cmd)` the reverse.
	// Only the first serves more than one connection; see the type's doc.
	openMode := uint32(windows.PIPE_ACCESS_INBOUND)
	maxInst := uint32(1)
	if substWrites {
		openMode = windows.PIPE_ACCESS_OUTBOUND
		maxInst = windows.PIPE_UNLIMITED_INSTANCES
	}
	for try := 0; ; try++ {
		native, shell := windowsProcSubstPipeNames(strconv.FormatUint(mathrand.Uint64(), 16))
		p := &procSubstNamedPipe{
			shellPath: shell,
			native:    native,
			openMode:  openMode,
			maxInst:   maxInst,
			stop:      make(chan struct{}),
		}
		// FILE_FLAG_FIRST_PIPE_INSTANCE makes the name's creation
		// exclusive; later instances of the same name must not repeat it.
		h, err := p.createInstance(true)
		if err == nil {
			p.handle = h
			return p, nil
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

// createInstance creates one server instance of the pipe's name. Every
// instance must agree on the mode, type and instance count, so only the
// first carries FILE_FLAG_FIRST_PIPE_INSTANCE.
func (p *procSubstNamedPipe) createInstance(first bool) (windows.Handle, error) {
	name16, err := windows.UTF16PtrFromString(p.native)
	if err != nil {
		return windows.InvalidHandle, err
	}
	mode := p.openMode
	if first {
		mode |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	return windows.CreateNamedPipe(name16, mode,
		windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
		p.maxInst, 4096, 4096, 0, nil)
}

func (p *procSubstNamedPipe) path() string { return p.shellPath }

// connectInstance blocks until a consumer opens the substituted path, like
// the blocking FIFO open on Unix. A client that connected between
// CreateNamedPipe and here is reported as ERROR_PIPE_CONNECTED.
func connectInstance(h windows.Handle) error {
	err := windows.ConnectNamedPipe(h, nil)
	if err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return err
	}
	return nil
}

// openWriter opens the `<(cmd)` substitution's write end. The returned file
// is not the pipe itself but the shell's copy of the body, which every
// connection is served from; see the type's doc.
func (p *procSubstNamedPipe) openWriter() (*os.File, error) {
	if err := connectInstance(p.handle); err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	p.handedTo = true
	p.body = newProcSubstBody(procSubstReplayCap)
	go func() {
		p.body.fill(r)
		r.Close()
	}()
	// The next instance is listening before this call returns, so a
	// consumer that opens the path again straight away — which is the
	// whole point — never finds every instance busy.
	next, nextErr := p.createInstance(false)
	go p.serveClient(p.handle)
	if nextErr == nil {
		go p.accept(next)
	}
	return w, nil
}

// openReader opens the `>(cmd)` substitution's read end: the single
// connection is handed over directly, as it always was.
func (p *procSubstNamedPipe) openReader() (*os.File, error) {
	if err := connectInstance(p.handle); err != nil {
		return nil, err
	}
	p.handedTo = true
	return os.NewFile(uintptr(p.handle), p.shellPath), nil
}

// accept keeps one instance listening so a second CreateFile on the name
// does not find every instance busy, and serves each connection that
// arrives until cleanup stops it.
func (p *procSubstNamedPipe) accept(h windows.Handle) {
	for {
		if err := connectInstance(h); err != nil {
			windows.CloseHandle(h)
			return
		}
		select {
		case <-p.stop:
			// cleanup's own connect released us; the rendezvous is over.
			windows.DisconnectNamedPipe(h)
			windows.CloseHandle(h)
			return
		default:
		}
		// Replace the instance we are about to consume first, so the name
		// keeps an unconnected instance for the next opener.
		next, err := p.createInstance(false)
		go p.serveClient(h)
		if err != nil {
			return
		}
		h = next
	}
}

// serveClient writes the whole body to one connection and then hangs it up,
// which is the EOF the consumer is waiting for.
func (p *procSubstNamedPipe) serveClient(h windows.Handle) {
	defer func() {
		windows.FlushFileBuffers(h)
		windows.DisconnectNamedPipe(h)
		windows.CloseHandle(h)
	}()
	rd := p.body.newReader()
	defer rd.Close()
	buf := make([]byte, 32*1024)
	for {
		n, err := rd.Read(buf)
		if n > 0 {
			if werr := writeAllHandle(h, buf[:n]); werr != nil {
				return // the consumer hung up first
			}
		}
		if err != nil {
			return
		}
	}
}

func writeAllHandle(h windows.Handle, b []byte) error {
	for len(b) > 0 {
		var done uint32
		if err := windows.WriteFile(h, b, &done, nil); err != nil {
			return err
		}
		if done == 0 {
			return windows.ERROR_BROKEN_PIPE
		}
		b = b[done:]
	}
	return nil
}

func (p *procSubstNamedPipe) cleanup() {
	p.stopOnce.Do(func() {
		close(p.stop)
		if p.body != nil {
			// A substitution still writing fails as it would on a broken
			// pipe, and the serving goroutines stop where they are.
			p.body.abort()
			// Release the accept loop's pending ConnectNamedPipe by
			// connecting to it ourselves; it sees stop and exits, taking
			// the last listening instance — and so the name — with it.
			if name16, err := windows.UTF16PtrFromString(p.native); err == nil {
				h, err := windows.CreateFile(name16, windows.GENERIC_READ, 0, nil,
					windows.OPEN_EXISTING, 0, 0)
				if err == nil {
					windows.CloseHandle(h)
				}
			}
		}
	})
	// The named pipe object disappears with its last handle. Once connect
	// handed the first instance over, its owner closes it.
	if !p.handedTo {
		windows.CloseHandle(p.handle)
	}
}
