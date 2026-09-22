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
	"time"

	"golang.org/x/sys/windows"
)

// procSubstNamedPipe backs process substitution on Windows with a
// \\.\pipe\ named pipe: the shell holds the server end, and the consumer —
// an external command or the shell's own redirection — opens the substituted
// path with a plain CreateFile, which os.OpenFile already performs. This is
// the smallest seam that makes `<(cmd)` and `>(cmd)` work without fd
// inheritance or /dev/fd emulation.
//
// # What a second open observes
//
// Unlike a FIFO, every CreateFile on a pipe's name is a *connection* rather
// than a second handle on one file, and a single-instance pipe therefore
// refused the second one outright with ERROR_PIPE_BUSY, "All pipe instances
// are busy" — which is what `diff <(a) <(b)` hit, its operands being stat'ed
// and then opened. Refusing is wrong; the question is what to answer
// instead.
//
// Bash answers it on Linux, where `<(cmd)` is not a FIFO at all but
// /dev/fd/N, one pipe the shell holds open. Opening that path again is
// another handle on the *same* stream, continuing from wherever the last
// reader left it — so once the first reader has drained the substitution,
// every later open reads end-of-stream. That is exactly what bash's own
// procsub test pins: five reads of one `<(date)` print 1, 0, 0, 0, 0.
//
// This is that rule, not a replay. The name keeps
// [procSubstPipeListeners] instances waiting, so a second open never finds
// them all busy; the *first* connection to arrive is the substitution's
// stream and is handed to the runner, and every connection after it is hung
// up unwritten, which the client reads as EOF. An open that reads nothing
// therefore costs nothing, and an open that reads gets either the whole body
// (it was first) or end-of-stream (it was not) — never an error.
//
// Hanging up means closing the instance, never DisconnectNamedPipe: a
// disconnect leaves the client's next read with ERROR_PIPE_NOT_CONNECTED,
// "No process is on the other end of the pipe", where closing gives it the
// ERROR_BROKEN_PIPE that every runtime reads as EOF.
//
// # After cleanup
//
// cleanup takes the listening instances away with the name, so an open that
// comes after the substitution finished gets ERROR_FILE_NOT_FOUND, and the
// shell's own stat of the path reports it gone (procSubstPipeStat) — both
// the way the unlinked FIFO behaves on Unix.
//
// A stat by a separate process (`ls -l` on the path) is also an opener, and
// is served like any other second open; the shell's own stat/test of the
// path never touches the pipe.
type procSubstNamedPipe struct {
	// shellPath is the //./pipe/ spelling substituted into the command
	// line; native is the \\.\pipe\ spelling CreateNamedPipe and CreateFile
	// are given, and name is the basename both share, which is the key of
	// the live registry (windowsProcSubstPipeNames).
	shellPath string
	native    string
	name      string

	// openMode is PIPE_ACCESS_OUTBOUND for `<(cmd)`, where the substitution
	// writes, and PIPE_ACCESS_INBOUND for `>(cmd)`, where it reads.
	openMode uint32

	// winner carries the first accepted connection to openWriter/openReader.
	winner chan windows.Handle
	stopCh chan struct{}

	mu sync.Mutex
	// first is the instance created with FILE_FLAG_FIRST_PIPE_INSTANCE,
	// until the accept loop takes it over.
	first windows.Handle
	// listening counts instances waiting in ConnectNamedPipe, which is what
	// cleanup has to release before the name can go away.
	listening int
	serving   bool
	claimed   bool
	stopped   bool
}

// procSubstPipeListeners is how many server instances of one substitution's
// name wait for a connection at a time. More than one so that a consumer
// which opens the path twice in a row — a stat followed by the open it was
// checking, say — never finds every instance busy in the window where the
// accept loop is replacing the one it just took.
const procSubstPipeListeners = 2

func (r *Runner) newProcSubstPipe(substWrites bool) (procSubstPipe, error) {
	// The server end's direction is fixed at creation: for `<(cmd)` the
	// substitution writes and the consumer reads, for `>(cmd)` the reverse.
	openMode := uint32(windows.PIPE_ACCESS_INBOUND)
	if substWrites {
		openMode = windows.PIPE_ACCESS_OUTBOUND
	}
	for try := 0; ; try++ {
		suffix := strconv.FormatUint(mathrand.Uint64(), 16)
		native, shell := windowsProcSubstPipeNames(suffix)
		p := &procSubstNamedPipe{
			shellPath: shell,
			native:    native,
			name:      fifoNamePrefix + suffix,
			openMode:  openMode,
			winner:    make(chan windows.Handle, 1),
			stopCh:    make(chan struct{}),
			first:     windows.InvalidHandle,
		}
		// FILE_FLAG_FIRST_PIPE_INSTANCE makes the name's creation
		// exclusive; the further instances of the same name must not
		// repeat it.
		h, err := p.createInstance(true)
		if err == nil {
			p.first = h
			procSubstPipeRegister(p.name)
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
// instance must agree on the mode and type, so only the first carries
// FILE_FLAG_FIRST_PIPE_INSTANCE.
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
		windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, nil)
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

// connect starts serving the name and blocks until the first consumer opens
// the substituted path, handing that connection over as an *os.File — whose
// Close also releases the handle, as it always did. Both directions take it:
// the substitution's own end is the first connection either way.
func (p *procSubstNamedPipe) connect() (*os.File, error) {
	p.startServing()
	select {
	case h := <-p.winner:
		return os.NewFile(uintptr(h), p.shellPath), nil
	case <-p.stopCh:
		return nil, windows.ERROR_BROKEN_PIPE
	}
}

func (p *procSubstNamedPipe) openWriter() (*os.File, error) { return p.connect() }

func (p *procSubstNamedPipe) openReader() (*os.File, error) { return p.connect() }

func (p *procSubstNamedPipe) startServing() {
	p.mu.Lock()
	start := !p.serving && !p.stopped
	p.serving = start
	p.mu.Unlock()
	if start {
		go p.serve()
	}
}

// serve keeps [procSubstPipeListeners] instances of the name waiting for a
// connection, and decides what each connection that arrives observes: the
// first is the substitution's stream, every later one is end-of-stream.
//
// Which instance a client lands on is the kernel's choice, so the decision
// is made on the order connections are *accepted*, never on the identity of
// the instance that took one.
func (p *procSubstNamedPipe) serve() {
	connected := make(chan windows.Handle)
	pending := 0
	for {
		for pending < procSubstPipeListeners {
			h, ok := p.newListener()
			if !ok {
				break
			}
			pending++
			go func() {
				err := connectInstance(h)
				p.mu.Lock()
				p.listening--
				p.mu.Unlock()
				if err != nil {
					windows.CloseHandle(h)
					connected <- windows.InvalidHandle
					return
				}
				connected <- h
			}()
		}
		if pending == 0 {
			return
		}
		h := <-connected
		pending--

		p.mu.Lock()
		stopped := p.stopped
		claim := !stopped && !p.claimed && h != windows.InvalidHandle
		if claim {
			p.claimed = true
		}
		p.mu.Unlock()

		switch {
		case h == windows.InvalidHandle:
			// That instance failed; the loop replaces it.
		case claim:
			p.winner <- h // buffered, and filled at most once
		default:
			// A later open observes the stream at its end: hang up
			// without writing, which the client reads as EOF. Closing,
			// not disconnecting — see the type's doc.
			windows.CloseHandle(h)
		}
		if stopped {
			// cleanup is releasing the listeners; take what they report
			// and let the name go.
			for ; pending > 0; pending-- {
				if h := <-connected; h != windows.InvalidHandle {
					windows.CloseHandle(h)
				}
			}
			return
		}
	}
}

// newListener creates one more instance and counts it in, unless cleanup has
// already run.
func (p *procSubstNamedPipe) newListener() (windows.Handle, bool) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return windows.InvalidHandle, false
	}
	h := p.first
	p.first = windows.InvalidHandle
	p.mu.Unlock()

	if h == windows.InvalidHandle {
		var err error
		if h, err = p.createInstance(false); err != nil {
			return windows.InvalidHandle, false
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		windows.CloseHandle(h)
		return windows.InvalidHandle, false
	}
	p.listening++
	return h, true
}

func (p *procSubstNamedPipe) cleanup() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	first := p.first
	p.first = windows.InvalidHandle
	serving := p.serving
	p.mu.Unlock()

	close(p.stopCh)
	if first != windows.InvalidHandle {
		// Nothing ever connected, so the accept loop never took it.
		windows.CloseHandle(first)
	}
	if serving {
		p.releaseListeners()
	}
	// The named pipe object disappears with its last handle; once connect
	// handed the first connection over, its *os.File owns that one.
	procSubstPipeRelease(p.name)
}

// releaseListeners unblocks every instance still waiting in
// ConnectNamedPipe by connecting to it as a client: the accept goroutine
// wakes, sees that cleanup has run and closes its handle, so the name goes
// away with the last of them. A client connect is used rather than
// CancelIoEx because the waits are synchronous and were issued on other
// goroutines' threads.
func (p *procSubstNamedPipe) releaseListeners() {
	name16, err := windows.UTF16PtrFromString(p.native)
	if err != nil {
		return
	}
	// A client of an outbound pipe reads and a client of an inbound one
	// writes; asking for the wrong direction is denied.
	access := uint32(windows.GENERIC_READ)
	if p.openMode == windows.PIPE_ACCESS_INBOUND {
		access = windows.GENERIC_WRITE
	}
	for try := 0; try < 200; try++ {
		p.mu.Lock()
		waiting := p.listening
		p.mu.Unlock()
		if waiting == 0 {
			return
		}
		h, err := windows.CreateFile(name16, access, 0, nil,
			windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			windows.CloseHandle(h)
			continue
		}
		// ERROR_PIPE_BUSY: an instance was counted out but has not yet
		// decremented. Give it the moment it needs.
		time.Sleep(time.Millisecond)
	}
}
