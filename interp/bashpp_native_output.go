package interp

// Sprint: #153; Story: S153.2; Story-ID: 7f74c9ff55b9
//
// Ordered output across the dependency boundary. The helper process writes the
// program's stdout/stderr itself; the interpreter writes its own output
// (println, panic reports) directly to the caller's writers. When a caller
// writer is not a real file the child's bytes travel through a pipe, so a
// request could be answered on the control channel before its output had
// drained, and the interpreter's next direct write would overtake it. Each
// drain owns that pipe and supports a barrier: the host writes a random
// sentinel into the same pipe, and once the copier has consumed the sentinel,
// every child byte written before the request's reply has already reached the
// caller's writer. Real files need no drain — child and interpreter writes
// share one kernel object and order themselves.

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"sync"
)

type bashPPNativeOutputDrain struct {
	read     *os.File
	write    *os.File
	sink     io.Writer
	sentinel []byte
	// mu serializes barriers so at most one sentinel is in flight.
	mu        sync.Mutex
	reached   chan struct{} // one token per consumed sentinel
	finished  chan struct{} // closed when the copier stops
	closeOnce sync.Once
}

func newBashPPNativeOutputDrain(sink io.Writer) (*bashPPNativeOutputDrain, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	sentinel := make([]byte, 16)
	if _, err := rand.Read(sentinel); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}
	d := &bashPPNativeOutputDrain{
		read: r, write: w, sink: sink, sentinel: sentinel,
		reached: make(chan struct{}, 1), finished: make(chan struct{}),
	}
	go d.copy()
	return d, nil
}

// copy forwards child output to the caller's writer, stripping each sentinel
// occurrence and acknowledging it. Only a strict sentinel prefix at the end of
// the buffer is withheld, and never past the barrier that resolves it.
func (d *bashPPNativeOutputDrain) copy() {
	defer close(d.finished)
	defer d.read.Close()
	var held []byte
	buf := make([]byte, 32*1024)
	for {
		n, err := d.read.Read(buf)
		if n > 0 {
			held = append(held, buf[:n]...)
			for {
				i := bytes.Index(held, d.sentinel)
				if i < 0 {
					break
				}
				if !d.flush(held[:i]) {
					return
				}
				held = append(held[:0], held[i+len(d.sentinel):]...)
				select {
				case d.reached <- struct{}{}:
				default:
				}
			}
			keep := 0
			for k := min(len(d.sentinel)-1, len(held)); k > 0; k-- {
				if bytes.Equal(held[len(held)-k:], d.sentinel[:k]) {
					keep = k
					break
				}
			}
			if !d.flush(held[:len(held)-keep]) {
				return
			}
			held = append(held[:0], held[len(held)-keep:]...)
		}
		if err != nil {
			d.flush(held)
			return
		}
	}
}

func (d *bashPPNativeOutputDrain) flush(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	_, err := d.sink.Write(data)
	return err == nil
}

// barrier returns once every byte the child wrote to this stream before the
// call has reached the caller's writer. It never blocks past copier
// termination, so a failed sink or a dead child cannot hang a request.
func (d *bashPPNativeOutputDrain) barrier() {
	d.mu.Lock()
	defer d.mu.Unlock()
	select {
	case <-d.reached:
	default:
	}
	if _, err := d.write.Write(d.sentinel); err != nil {
		return
	}
	select {
	case <-d.reached:
	case <-d.finished:
	}
}

// closeWrite retires the host's write end. Once the child is gone too, the
// copier sees EOF, flushes what remains and finishes.
func (d *bashPPNativeOutputDrain) closeWrite() {
	d.closeOnce.Do(func() { d.write.Close() })
}

func (s *bashPPNativeSession) drainOutputs() {
	for _, drain := range s.drains {
		drain.barrier()
	}
}

func (s *bashPPNativeSession) closeDrains() {
	for _, drain := range s.drains {
		drain.closeWrite()
	}
}

// waitDrains blocks until every copier has flushed its remaining output, so a
// terminated program's final bytes are visible before the interpreter reports
// its exit.
func (s *bashPPNativeSession) waitDrains() {
	for _, drain := range s.drains {
		<-drain.finished
	}
}

// bashPPSameWriter mirrors os/exec's interfaceEqual: one writer given for both
// standard streams gets one pipe, preserving the child's own interleaving.
func bashPPSameWriter(a, b io.Writer) (same bool) {
	if a == nil || b == nil {
		return false
	}
	defer func() { _ = recover() }()
	return a == b
}
