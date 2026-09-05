// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
)

type bashPPFIFOIdentity struct{ dev, ino uint64 }

// Writer adapters must not turn an owned native descriptor into a borrowed
// pointer at the task boundary. Rebuild known adapters around the same dup
// cache used by readable fds, preserving aliases and wrapper semantics.
func bashPPDupWriter(w io.Writer, child *Runner, dup func(*os.File) (*os.File, error)) (io.Writer, error) {
	switch f := w.(type) {
	case *os.File:
		return dup(f)
	case fifoWriteFile:
		file, err := dup(f.File)
		return fifoWriteFile{File: file, path: f.path}, err
	case borrowedFile:
		file, err := dup(f.File)
		return borrowedFile{File: file}, err
	case *bashPPLockedWriter:
		writer, err := bashPPDupWriter(f.w, child, dup)
		return &bashPPLockedWriter{mu: f.mu, w: writer}, err
	case *pipelineWriter:
		writer, err := bashPPDupWriter(f.w, child, dup)
		return &pipelineWriter{w: writer, runner: child}, err
	default:
		return w, nil
	}
}

type bashPPFIFOEntry struct {
	group       *bashPPConcurrent
	owner       *Runner
	key         bashPPFIFOIdentity
	file, probe *os.File
	path        string
	read, write bool
	ready       chan struct{}
	matched     bool // protected by group.fifoMu
	closed      bool // protected by group.fifoMu
	once        sync.Once
}

// A native peer is deliberately insufficient: only descriptors registered in
// the same File task group may release a Bash++ FIFO open. In particular an
// external writer must not turn an unmatched read into premature EOF.
func (r *Runner) bashPPFIFOOpen(ctx context.Context, path string, flags int) (*os.File, bool, error) {
	file, probe, key, fifo, err := bashPPFIFOAcquire(r.bashPPTaskContext(ctx), r.dirFile, r.Dir, path, flags)
	if !fifo || err != nil {
		return nil, fifo, err
	}
	// Configuration failure remains in the synchronous prefix. Do not publish
	// an endpoint or release an opposite opener until its descriptor is ready.
	if err := bashPPTaskSourceClearNonblock(file); err != nil {
		_ = file.Close()
		if probe != nil {
			_ = probe.Close()
		}
		return nil, true, err
	}
	// Owner FIFO descriptors opened before the first go/chan declaration
	// still belong to this File, and must survive into task snapshots.
	c := r.bashPPConcurrency(ctx)
	e := &bashPPFIFOEntry{group: c, owner: r, key: key, file: file, probe: probe, path: path,
		read: flags&os.O_WRONLY == 0, write: flags&(os.O_WRONLY|os.O_RDWR) != 0,
		ready: make(chan struct{})}
	c.fifoMu.Lock()
	if c.fifos == nil {
		c.fifos = make(map[*os.File]*bashPPFIFOEntry)
	}
	c.fifos[file] = e
	for _, peer := range c.fifos {
		if peer.key == key && !peer.closed && ((e.read && peer.write) || (e.write && peer.read)) {
			for _, match := range []*bashPPFIFOEntry{e, peer} {
				if !match.matched {
					match.matched = true
					if match.probe != nil {
						_ = match.probe.Close()
						match.probe = nil
					}
					close(match.ready)
				}
			}
		}
	}
	matched := e.matched
	c.fifoMu.Unlock()
	if !matched && !r.bashPPArmBeforeBlock(ctx) {
		_ = e.Close()
		return nil, true, c.ctx.Err()
	}
	select {
	case <-e.ready:
	case <-c.ctx.Done():
		_ = e.Close()
		return nil, true, fmt.Errorf("FIFO requires a registered peer in this Bash++ task group; external or unregistered peers are unsupported: %w", c.ctx.Err())
	}
	if err := c.ctx.Err(); err != nil {
		_ = e.Close()
		return nil, true, err
	}
	return file, true, nil
}

func (e *bashPPFIFOEntry) Close() error {
	var err error
	e.once.Do(func() {
		e.group.fifoMu.Lock()
		e.closed = true
		delete(e.group.fifos, e.file)
		if e.probe != nil {
			_ = e.probe.Close()
			e.probe = nil
		}
		if e.write && e.matched {
			_ = refreshFileTimesNow(e.file, e.path)
		}
		err = e.file.Close()
		e.group.fifoMu.Unlock()
	})
	return err
}

func (r *Runner) bashPPFIFOCloser(closer io.Closer) io.Closer {
	if r.bashPPConcurrent == nil {
		return closer
	}
	var file *os.File
	switch f := closer.(type) {
	case *os.File:
		file = f
	case fifoWriteFile:
		file = f.File
	default:
		return closer
	}
	c := r.bashPPConcurrent
	c.fifoMu.Lock()
	e := c.fifos[file]
	c.fifoMu.Unlock()
	if e != nil {
		return bashPPFIFOStatementCloser{r, e}
	}
	return closer
}

// Redirection scopes borrow their descriptors. An exec alias can escape the
// scope, so its deferred closer must not revoke a still-live virtual binding.
// Cancellation and task/group teardown use Entry.Close directly instead.
type bashPPFIFOStatementCloser struct {
	r *Runner
	e *bashPPFIFOEntry
}

func (c bashPPFIFOStatementCloser) Close() error {
	if !c.r.bashPPFIFORefs()[c.e.file] {
		return c.e.Close()
	}
	return nil
}

func bashPPFIFORef(refs map[*os.File]bool, value any) {
	switch f := value.(type) {
	case *os.File:
		if f != nil {
			refs[f] = true
		}
	case fifoWriteFile:
		bashPPFIFORef(refs, f.File)
	case borrowedFile:
		bashPPFIFORef(refs, f.File)
	case *bashPPLockedWriter:
		bashPPFIFORef(refs, f.w)
	case *pipelineWriter:
		bashPPFIFORef(refs, f.w)
	}
}

// Only the owning runner reads its bindings. Task snapshots register independent
// OS duplicates, so retiring an original never revokes a task's authority.
func (r *Runner) bashPPFIFORefs() map[*os.File]bool {
	refs := make(map[*os.File]bool)
	bashPPFIFORef(refs, r.stdin)
	bashPPFIFORef(refs, r.stdout)
	bashPPFIFORef(refs, r.stderr)
	for _, f := range r.fdTable {
		bashPPFIFORef(refs, f)
	}
	for _, w := range r.fdWriteTable {
		bashPPFIFORef(refs, w)
	}
	for _, scope := range r.redirScopes {
		if !scope.persist && scope.fifoRestoreRefs != nil {
			scope.fifoRestoreRefs(refs)
		}
	}
	return refs
}

func (r *Runner) bashPPReconcileFIFOs() {
	c := r.bashPPConcurrent
	if c == nil {
		return
	}
	refs := r.bashPPFIFORefs()
	c.fifoMu.Lock()
	var retired []*bashPPFIFOEntry
	for _, e := range c.fifos {
		if e.owner == r && !refs[e.file] {
			retired = append(retired, e)
		}
	}
	c.fifoMu.Unlock()
	for _, e := range retired {
		_ = e.Close()
	}
}

func (c *bashPPConcurrent) closeFIFOs(owner *Runner) {
	c.fifoMu.Lock()
	var entries []*bashPPFIFOEntry
	for _, e := range c.fifos {
		if owner == nil || e.owner == owner {
			entries = append(entries, e)
		}
	}
	c.fifoMu.Unlock()
	for _, e := range entries {
		_ = e.Close()
	}
}

// A task snapshot owns its duplicate, even after the opening statement or
// another task closes the original descriptor. Preserve the registration for
// the duplicate's lifetime without retaining an extra kernel descriptor.
func (c *bashPPConcurrent) cloneFIFO(original, duplicate *os.File, owner *Runner) {
	c.fifoMu.Lock()
	defer c.fifoMu.Unlock()
	source := c.fifos[original]
	if source == nil || source.closed {
		return
	}
	e := &bashPPFIFOEntry{group: c, owner: owner, key: source.key, file: duplicate, path: source.path,
		read: source.read, write: source.write, ready: make(chan struct{}), matched: true}
	close(e.ready)
	c.fifos[duplicate] = e
}
