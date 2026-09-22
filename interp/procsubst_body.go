// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"io"
	"sync"
)

// procSubstReplayCap bounds the window of a process substitution's output
// the shell keeps in memory (see [procSubstBody]). While the whole body
// still fits, every consumer that opens the substituted path is served the
// body from its first byte; past the cap the producer is throttled by the
// slowest consumer, as a pipe throttles its writer, and the oldest bytes
// are dropped once every live consumer is past them.
const procSubstReplayCap = 1 << 20 // 1 MiB

// procSubstBody is the shell's view of what one process substitution
// writes. It exists for Windows, where the rendezvous is a named pipe
// rather than a FIFO: every CreateFile on the pipe's name is a *connection*,
// not a second handle on one file, so a consumer that opens the substituted
// path twice — our Go `diff` does, for `diff <(t1) <(t2)` — finds the single
// server instance busy and fails with "All pipe instances are busy".
//
// The body decouples the two sides: the substitution writes here, and each
// connection gets its own reader over the same byte sequence. A reader that
// arrives while the body is still retained in full (the common case: a
// substitution's output is a file-sized payload) is replayed from byte zero
// and then follows the live stream to EOF, so two sequential opens both see
// the whole body. A reader that arrives after the body has outgrown
// [procSubstReplayCap] starts at the oldest byte still held.
//
// Back-pressure is preserved: the window never exceeds the cap, so a
// substitution writing more than that blocks until its slowest live
// consumer catches up, and blocks outright while no consumer is connected.
type procSubstBody struct {
	mu   sync.Mutex
	cond *sync.Cond

	// buf holds the stream bytes [start, end). start is 0 for as long as
	// the whole body is retained, which is what makes a replay possible.
	buf   []byte
	start int64
	end   int64

	// done is set when the substitution closed its end; closed when the
	// shell tore the rendezvous down. Either releases the readers.
	done   bool
	closed bool

	cap     int
	readers map[*procSubstBodyReader]struct{}
}

func newProcSubstBody(capBytes int) *procSubstBody {
	b := &procSubstBody{cap: capBytes, readers: make(map[*procSubstBodyReader]struct{})}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Write appends the substitution's output, blocking while the retained
// window is full and no reader has moved on.
func (b *procSubstBody) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := 0
	for len(p) > 0 {
		if b.closed {
			return written, io.ErrClosedPipe
		}
		room := b.cap - len(b.buf)
		if room <= 0 {
			if !b.evictLocked() {
				b.cond.Wait()
			}
			continue
		}
		n := min(room, len(p))
		b.buf = append(b.buf, p[:n]...)
		b.end += int64(n)
		p = p[n:]
		written += n
		b.cond.Broadcast()
	}
	return written, nil
}

// evictLocked drops the bytes every live reader is already past, reporting
// whether that freed any room. With no reader connected nothing can be
// dropped — the window is the only record of the body — so the writer
// waits, exactly as it would on a full pipe nobody is draining.
func (b *procSubstBody) evictLocked() bool {
	if len(b.readers) == 0 {
		return false
	}
	low := b.end
	for r := range b.readers {
		low = min(low, max(r.pos, b.start))
	}
	if low <= b.start {
		return false
	}
	b.buf = append(b.buf[:0], b.buf[low-b.start:]...)
	b.start = low
	b.cond.Broadcast()
	return true
}

// close marks the substitution's end closed: readers drain the body and
// then see EOF.
func (b *procSubstBody) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.done = true
	b.cond.Broadcast()
}

// abort tears the body down: the writer fails as it would on a broken pipe
// and every reader stops where it is. Used when the shell releases the
// rendezvous with a substitution still writing.
func (b *procSubstBody) abort() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.cond.Broadcast()
}

// fill copies a substitution's output into the body until its end closes.
func (b *procSubstBody) fill(r io.Reader) {
	_, _ = io.Copy(b, r)
	b.close()
}

// newReader opens one consumer's view of the body: the whole body when it
// is still retained from its first byte, otherwise the oldest byte still
// held.
func (b *procSubstBody) newReader() *procSubstBodyReader {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := &procSubstBodyReader{b: b, pos: b.start}
	b.readers[r] = struct{}{}
	return r
}

// procSubstBodyReader is one connection's read side of a [procSubstBody].
type procSubstBodyReader struct {
	b   *procSubstBody
	pos int64
}

func (r *procSubstBodyReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b := r.b
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		// A slow reader can be overtaken once the body outgrows the cap;
		// it resumes at the oldest byte still held.
		r.pos = max(r.pos, b.start)
		if r.pos < b.end {
			n := copy(p, b.buf[r.pos-b.start:])
			r.pos += int64(n)
			b.cond.Broadcast()
			return n, nil
		}
		if b.done || b.closed {
			return 0, io.EOF
		}
		b.cond.Wait()
	}
}

// Close releases the reader so the writer stops waiting on it.
func (r *procSubstBodyReader) Close() error {
	b := r.b
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.readers, r)
	b.cond.Broadcast()
	return nil
}
