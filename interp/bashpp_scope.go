// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"

	"mvdan.cc/sh/v3/expand"
)

// Lexical scopes for the Bash++ typed declarations.
//
// WHY A SECOND NAMESPACE AT ALL. A shell variable's lifetime is dynamic: it is
// created by an assignment, it outlives the compound command that created it,
// and a function sees its caller's locals. A Go declaration's lifetime is
// lexical: it begins at the declaration and ends with the enclosing block, and
// a function body sees only what was in scope where the function was written.
// The two rules disagree about `if true; then var x = 1; fi; echo $x`, so they
// cannot be the same namespace. Shell assignments keep bash's lifetime exactly;
// only names introduced by `var`/`const` live here.
//
// ONE NAMESPACE, MANY VIEWS. What must NOT differ is the VALUE a name has. A
// binding that expansion reads as 1 while an exec handler's Env reads as the
// shadowed shell value would be two variables wearing one name, and every
// consumer would have to know which one it got. So the scope chain is the
// single authority, and each read path is routed through it exactly once:
//
//   - expansion — [Runner.lookupVar], reached by expandEnv.Get
//   - name enumeration — expandEnv.Each, over [Runner.bashPPEnviron]
//   - HandlerCtx.Env — the same view, under the usual overlay
//   - external commands — execEnv over the same view
//   - writes — [Runner.setVar]
//   - deletes — [Runner.delVar]
//
// ISOLATION IS BY COPY, NOT BY LOCK. Subshells here are goroutines, not
// fork(), so a background subshell and its parent run concurrently in one
// address space. Sharing a cell pointer across that boundary is a data race
// the race detector reports, and sharing it is also wrong: bash gives a
// subshell a private copy of the whole variable space. [bashPPScope.clone]
// therefore deep-copies the chain, and [Runner.subshell] calls it for
// foreground and background subshells alike.

// bashPPCell is one binding's storage.
//
// It is a pointer target rather than a map value because a closure captures
// the CELL, not the value: a function defined inside a loop body must observe
// later mutations of the identifier it closed over, which is only possible if
// the snapshot and the live scope name the same cell.
type bashPPCell struct {
	vr expand.Variable
	// object is shared by every alias of one structured value. Deep readonly
	// is an attribute of this identity rather than of one variable spelling.
	object *bashPPObjectIdentity
	// typeName is non-empty for a value of a script-declared named type.
	// Pointer/nilPointer retain identity in-process; the visible shell value
	// remains vr, so typed values never need a lossy JSON representation.
	typeName   string
	pointer    bool
	nilPointer bool
	// constant marks a `const` binding. It is kept beside vr.ReadOnly rather
	// than derived from it because the shell's readonly machinery is what
	// vr.ReadOnly drives, and the two answer to different owners: `declare -r`
	// may set the latter, but only `const` sets this.
	constant bool
}

// bashPPScope is one lexical block's declarations, linked to its enclosing
// block. Lookup walks outwards; declaration only ever writes to the innermost.
type bashPPScope struct {
	parent  *bashPPScope
	entries map[string]*bashPPCell
}

func newBashPPScope(parent *bashPPScope) *bashPPScope {
	return &bashPPScope{parent: parent, entries: make(map[string]*bashPPCell)}
}

// lookup finds the innermost binding of name, or nil.
func (s *bashPPScope) lookup(name string) *bashPPCell {
	for at := s; at != nil; at = at.parent {
		if cell, ok := at.entries[name]; ok {
			return cell
		}
	}
	return nil
}

// declare introduces name in THIS block.
//
// Redeclaration in the same block is an error, as it is in Go; redeclaration
// in an inner block is ordinary shadowing and is allowed. This is what makes
// "the loop body got a fresh scope" observable from a script: a second
// iteration re-running `var i = 1` succeeds precisely because the first
// iteration's block is gone.
func (s *bashPPScope) declare(name string, vr expand.Variable, constant bool) error {
	if _, exists := s.entries[name]; exists {
		return fmt.Errorf("%s redeclared in this block", name)
	}
	vr.ReadOnly = vr.ReadOnly || constant
	s.entries[name] = &bashPPCell{vr: vr, constant: constant}
	return nil
}

// snapshot freezes which NAMES are visible while keeping the cells live.
//
// This is the whole of Go's "the scope of a declared identifier begins at the
// declaration" rule, and it is why the capture cannot simply be a pointer to
// the enclosing scope. A function defined before `var x = 1` runs in a block
// that does not contain x, so it must not see x — but a function defined after
// `var i = 1` and called after `i` was reassigned must see the NEW value.
// Copying the entry maps satisfies the first (later declarations land in the
// original map, not the copy) and sharing the cells satisfies the second.
func (s *bashPPScope) snapshot() *bashPPScope {
	if s == nil {
		return nil
	}
	out := newBashPPScope(s.parent.snapshot())
	for name, cell := range s.entries {
		out.entries[name] = cell
	}
	return out
}

// clone deep-copies the chain so a subshell shares nothing with its parent.
//
// The memo is not an optimization. A runner's live scope and the scopes it
// captured for its functions overlap: a closure's chain typically shares
// blocks, and after [bashPPScope.snapshot] it shares individual cells. Cloning
// each reachable scope independently would silently split those aliases, so a
// function that observed a variable before the subshell would stop observing
// it inside one. Memoizing on the ORIGINAL pointer reproduces the aliasing
// graph exactly, one edge at a time.
type bashPPCloner struct {
	scopes  map[*bashPPScope]*bashPPScope
	cells   map[*bashPPCell]*bashPPCell
	objects map[*bashPPObjectIdentity]*bashPPObjectIdentity
}

func newBashPPCloner() *bashPPCloner {
	return &bashPPCloner{
		scopes:  make(map[*bashPPScope]*bashPPScope),
		cells:   make(map[*bashPPCell]*bashPPCell),
		objects: make(map[*bashPPObjectIdentity]*bashPPObjectIdentity),
	}
}

func (c *bashPPCloner) clone(s *bashPPScope) *bashPPScope {
	if s == nil {
		return nil
	}
	if done, ok := c.scopes[s]; ok {
		return done
	}
	out := &bashPPScope{entries: make(map[string]*bashPPCell, len(s.entries))}
	// Record before recursing: the chain is acyclic today, but memoizing
	// first is what keeps that from being a precondition.
	c.scopes[s] = out
	out.parent = c.clone(s.parent)
	for name, cell := range s.entries {
		out.entries[name] = c.cloneCell(cell)
	}
	return out
}

func (c *bashPPCloner) cloneCell(cell *bashPPCell) *bashPPCell {
	if cell == nil {
		return nil
	}
	if copied, ok := c.cells[cell]; ok {
		return copied
	}
	dup := *cell
	if cell.object != nil {
		if object := c.objects[cell.object]; object != nil {
			dup.object = object
		} else {
			objectCopy := *cell.object
			dup.object = &objectCopy
			c.objects[cell.object] = dup.object
		}
	}
	copied := &dup
	c.cells[cell] = copied
	return copied
}

func cloneBashPPVariable(vr expand.Variable) expand.Variable {
	vr.List = append([]string(nil), vr.List...)
	if vr.ListMap != nil {
		src := vr.ListMap
		vr.ListMap = make(map[int]string, len(src))
		for key, value := range src {
			vr.ListMap[key] = value
		}
	}
	if vr.ListSet != nil {
		src := vr.ListSet
		vr.ListSet = make(map[int]bool, len(src))
		for key, value := range src {
			vr.ListSet[key] = value
		}
	}
	if vr.Map != nil {
		src := vr.Map
		vr.Map = make(map[string]string, len(src))
		for key, value := range src {
			vr.Map[key] = value
		}
	}
	return vr
}

// bashPPPushScope enters a new lexical block, returning the function that
// leaves it. Callers defer the result; the pair must bracket the block's
// statements and nothing else.
func (r *Runner) bashPPPushScope() func() {
	previous := r.bashPPScope
	r.bashPPScope = newBashPPScope(previous)
	return func() { r.bashPPScope = previous }
}

// bashPPEnviron presents the scope chain and the shell's variables as one
// environment, so that every consumer which enumerates variables — HandlerCtx,
// execEnv, `${!prefix@}` — sees the same values expansion does.
//
// Get prefers the innermost typed binding; Each yields the shell's variables
// first and the typed bindings last, which is what [expand.Environ] asks for
// when a name appears twice.
type bashPPEnviron struct {
	scope  *bashPPScope
	parent expand.Environ
}

var _ expand.Environ = bashPPEnviron{}

func (e bashPPEnviron) Get(name string) expand.Variable {
	if cell := e.scope.lookup(name); cell != nil {
		return cell.vr
	}
	return e.parent.Get(name)
}

func (e bashPPEnviron) Each(fn func(name string, vr expand.Variable) bool) {
	// Each shadowed name is yielded ONCE, with the typed value. [expand.Environ]
	// permits a repeated name and says the last one wins, but execEnv turns its
	// result into a child's environment block, and there the two entries would
	// both survive into execve — leaving which value the child reads up to
	// whichever end of the array its libc happens to scan from. Collecting the
	// visible bindings first is what keeps that from being a portability
	// question.
	visible := e.scope.visible()
	for name, vr := range e.parent.Each {
		if _, shadowed := visible[name]; shadowed {
			continue
		}
		if !fn(name, vr) {
			return
		}
	}
	for name, vr := range visible {
		if !fn(name, vr) {
			return
		}
	}
}

// visible flattens the chain into the bindings a lookup would find, with an
// inner declaration replacing the outer one it shadows.
func (s *bashPPScope) visible() map[string]expand.Variable {
	if s == nil {
		return nil
	}
	out := s.parent.visible()
	if out == nil {
		out = make(map[string]expand.Variable, len(s.entries))
	}
	for name, cell := range s.entries {
		out[name] = cell.vr
	}
	return out
}

// bashPPEnv returns the environment view every enumeration path must use. It
// is r.writeEnv itself whenever no typed binding can exist, so LangBash pays
// nothing for this.
func (r *Runner) bashPPEnv() expand.Environ {
	if r.bashPPScope == nil {
		return r.writeEnv
	}
	return bashPPEnviron{scope: r.bashPPScope, parent: r.writeEnv}
}
