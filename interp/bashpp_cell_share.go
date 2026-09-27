// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"sync"

	"mvdan.cc/sh/v3/expand"
)

// Host synchronization for a cell two interpreted goroutines can both name.
//
// WHY A LOCK EXISTS AT ALL. [bashPPScope] isolates a subshell BY COPY, so no
// lock is needed there. A Go `go` statement is the opposite: the task and its
// launcher must name ONE variable for a package-scope `var` or a captured
// local, so [bashPPCloner.cloneCell] deliberately aliases the cell rather than
// copying it. That makes the cell's fields reachable from two host goroutines.
//
// WHY THE GUEST'S OWN RULES ARE NOT ENOUGH. A Go program that writes such a
// variable from one goroutine while another reads it has a data race, and Go
// leaves the value it reads unspecified — any previously written value is a
// legal answer. What Go does NOT leave unspecified is the HOST: one guest
// `seqno++` is a dozen separate stores into a *bashPPCell, including a string
// header and an interface word. A reader that interleaves with those stores
// can observe a half-updated cell, which is not "some value the program wrote"
// but a torn Go string — undefined behaviour in the interpreter itself, and the
// data race the detector reports against interp rather than against the script.
//
// SO: an aliased cell carries a guard, writers publish their whole field bundle
// under it, and readers take a consistent snapshot under it and then work on
// that snapshot. The guest keeps its unspecified-but-harmless race; the host
// gets a well-defined one of the values that were actually stored. A cell that
// was never aliased has a nil guard and pays nothing.

// shareGuard arms cell for concurrent host access.
//
// It is called only from [bashPPCloner.cloneCell] while it aliases a cell into
// a task snapshot, which always runs in the launching goroutine before that
// task's goroutine starts. The field is therefore written only on a cell's
// FIRST aliasing — when no other goroutine can yet reach it — and is only read
// afterwards.
func (c *bashPPCell) shareGuard() {
	if c != nil && c.guard == nil {
		c.guard = new(sync.Mutex)
	}
}

// lock and unlock bracket a writer publishing c's fields. They are no-ops for
// an unaliased cell. A bracketed region must not evaluate guest code, take a
// second cell's guard, or block: the guard is held across field stores only.
func (c *bashPPCell) lock() {
	if c != nil && c.guard != nil {
		c.guard.Lock()
	}
}

func (c *bashPPCell) unlock() {
	if c != nil && c.guard != nil {
		c.guard.Unlock()
	}
}

// view returns a cell whose fields a reader may examine without a lock: c
// itself when it was never aliased, otherwise a snapshot taken under the guard.
//
// The snapshot is shallow on purpose. It copies the fields a writer publishes
// as a bundle — the expand.Variable header, the scalar carrier flags, the type
// and identity edges — so no reader sees a torn value. The pointers it copies
// (object identity, collection meta, interface payload) keep their identity, so
// a view is still the same value the cell names.
func (c *bashPPCell) view() *bashPPCell {
	if c == nil || c.guard == nil {
		return c
	}
	c.guard.Lock()
	dup := *c
	c.guard.Unlock()
	// The snapshot is private to one goroutine, so reads of it need no guard,
	// and clearing the field keeps a nested view() from locking again.
	dup.guard = nil
	return &dup
}

// viewVar is [bashPPCell.view] for a caller that needs only the shell value,
// without copying the whole cell.
func (c *bashPPCell) viewVar() expand.Variable {
	if c == nil {
		return expand.Variable{}
	}
	if c.guard == nil {
		return c.vr
	}
	c.guard.Lock()
	defer c.guard.Unlock()
	return c.vr
}
