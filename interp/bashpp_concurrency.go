// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

const (
	bashPPChanHandlePrefix = "chan@bashpp:"
	bashPPMaxChanCapacity  = 65536
)

type bashPPChannel struct {
	elem string
	ch   chan string
}

type bashPPTaskFailure struct {
	code uint8
	text string
}

// One dynamic structured task group belongs to one owning File Run. Unlike a
// sync.WaitGroup, registration remains safe while the owner is joining; only
// reaching zero permanently quiesces the group.
type bashPPConcurrent struct {
	mu       sync.Mutex
	changed  *sync.Cond
	ctx      context.Context
	cancel   context.CancelFunc
	active   int
	quiesced bool
	first    *bashPPTaskFailure
	next     int
	chans    map[string]*bashPPChannel
	ioMu     sync.Mutex
}

var bashPPConcurrencyInitMu sync.Mutex

func newBashPPConcurrent(parent context.Context) *bashPPConcurrent {
	ctx, cancel := context.WithCancel(parent)
	c := &bashPPConcurrent{ctx: ctx, cancel: cancel, chans: make(map[string]*bashPPChannel)}
	c.changed = sync.NewCond(&c.mu)
	return c
}

func (r *Runner) bashPPConcurrency(ctx context.Context) *bashPPConcurrent {
	bashPPConcurrencyInitMu.Lock()
	defer bashPPConcurrencyInitMu.Unlock()
	if r.bashPPConcurrent == nil {
		r.bashPPConcurrent = newBashPPConcurrent(ctx)
	}
	return r.bashPPConcurrent
}

func (c *bashPPConcurrent) add() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quiesced || c.ctx.Err() != nil {
		return false
	}
	c.active++
	return true
}

func (c *bashPPConcurrent) done(f *bashPPTaskFailure) {
	c.mu.Lock()
	if f != nil && c.first == nil {
		copy := *f
		c.first = &copy
		c.cancel()
	}
	c.active--
	if c.active == 0 {
		c.changed.Broadcast()
	}
	c.mu.Unlock()
}

func (c *bashPPConcurrent) stopAndJoin() *bashPPTaskFailure {
	c.cancel()
	c.mu.Lock()
	for c.active != 0 {
		c.changed.Wait()
	}
	c.quiesced = true
	var result *bashPPTaskFailure
	if c.first != nil {
		copy := *c.first
		result = &copy
	}
	c.mu.Unlock()
	return result
}

func (r *Runner) bashPPTaskContext(ctx context.Context) context.Context {
	if r.bashPPConcurrent != nil {
		return r.bashPPConcurrent.ctx
	}
	return ctx
}

func (r *Runner) bashPPChannel(w *syntax.Word) (*bashPPChannel, bool) {
	name := r.literal(w)
	if r.bashPPConcurrent == nil {
		r.errf("bash++: %s is not a channel in this task group\n", name)
		r.exit.code = 2
		return nil, false
	}
	if r.bashPPChanBoundary {
		r.errf("bash++: channel %s cannot cross a shell-copy boundary\n", name)
		r.exit.code = 2
		return nil, false
	}
	vr := r.lookupVar(name)
	c := r.bashPPConcurrent
	c.mu.Lock()
	h, ok := c.chans[vr.Str]
	c.mu.Unlock()
	if !ok {
		r.errf("bash++: %s is not a channel in this task group\n", name)
		r.exit.code = 2
	}
	return h, ok
}

func (r *Runner) bashPPMakeChan(ctx context.Context, d *syntax.BashPPShortDecl) {
	if len(d.Lhs) != 1 {
		r.errf("assignment mismatch: make(chan) has one value\n")
		r.exit.code = 2
		return
	}
	capacity := 0
	if d.MakeChan.Capacity != nil {
		var err error
		capacity, err = strconv.Atoi(r.literal(d.MakeChan.Capacity))
		if err != nil || capacity < 0 || capacity > bashPPMaxChanCapacity {
			r.errf("make(chan): capacity must be between 0 and %d\n", bashPPMaxChanCapacity)
			r.exit.code = 2
			return
		}
	}
	c := r.bashPPConcurrency(ctx)
	c.mu.Lock()
	h := bashPPChanHandlePrefix + strconv.Itoa(c.next)
	c.next++
	c.chans[h] = &bashPPChannel{elem: d.MakeChan.ChanType.Elem.Value, ch: make(chan string, capacity)}
	c.mu.Unlock()
	r.bashPPDeclareName(d.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: h})
}

func (r *Runner) bashPPSend(ctx context.Context, s *syntax.BashPPSend) {
	c, ok := r.bashPPChannel(s.Chan)
	if !ok {
		return
	}
	v := r.literal(s.Value)
	if !r.bashPPValueFits(c.elem, v) {
		r.errf("bash++: cannot send %q as %s channel value\n", v, c.elem)
		r.exit.code = 2
		return
	}
	defer func() {
		if recover() != nil {
			r.errf("bash++: send on closed channel\n")
			r.exit.code = 2
		}
	}()
	select {
	case c.ch <- v:
	case <-r.bashPPTaskContext(ctx).Done():
		r.exit.code = 1
	}
}

func (r *Runner) bashPPReceive(ctx context.Context, recv *syntax.BashPPReceive, lhs []*syntax.Lit) (string, bool) {
	c, ok := r.bashPPChannel(recv.Chan)
	if !ok {
		return "", false
	}
	var v string
	var open bool
	select {
	case v, open = <-c.ch:
	case <-r.bashPPTaskContext(ctx).Done():
		r.exit.code = 1
		return "", false
	}
	if lhs != nil {
		if len(lhs) == 0 || len(lhs) > 2 {
			r.errf("receive assignment mismatch\n")
			r.exit.code = 2
			return v, open
		}
		r.bashPPDeclareName(lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: v})
		if len(lhs) == 2 {
			r.bashPPDeclareName(lhs[1].Value, expand.Variable{Set: true, Kind: expand.String, Str: strconv.FormatBool(open)})
		}
	}
	return v, open
}

func (r *Runner) bashPPClose(cl *syntax.BashPPClose) {
	c, ok := r.bashPPChannel(cl.Chan)
	if !ok {
		return
	}
	defer func() {
		if recover() != nil {
			r.errf("bash++: close of closed channel\n")
			r.exit.code = 2
		}
	}()
	close(c.ch)
}

// bashPPTaskSnapshot is the explicit task boundary. subshell(true) clones
// variables, cwd, options, functions/types/imports, traps/signals/jobs and fd
// maps. The maps are task-owned; underlying OS open-file descriptions retain
// their normal shared offsets. Only the group and channel cores are shared.
func (r *Runner) bashPPTaskSnapshot() *Runner {
	child := r.subshell(true)
	child.bashPPConcurrent, child.bashPPGoTask, child.bashPPChanBoundary = r.bashPPConcurrent, true, false
	return child
}

func (r *Runner) bashPPGo(ctx context.Context, g *syntax.BashPPGo) {
	if g.Call == nil {
		return
	}
	c := r.bashPPConcurrency(ctx)
	if !c.add() {
		r.errf("bash++: cannot start task after task group quiesced\n")
		r.exit.code = 1
		return
	}
	child := r.bashPPTaskSnapshot()
	go func() {
		var failure *bashPPTaskFailure
		defer func() {
			if x := recover(); x != nil {
				failure = &bashPPTaskFailure{code: 2, text: fmt.Sprintf("panic: %v", x)}
			}
			child.stopSignalSubscriptions()
			if child.dirFile != nil {
				_ = child.dirFile.Close()
				child.dirFile = nil
			}
			c.done(failure)
		}()
		child.bashPPCall(c.ctx, g.Call)
		if child.exit.code != 0 {
			failure = &bashPPTaskFailure{code: child.exit.code, text: fmt.Sprintf("exit status %d", child.exit.code)}
		}
	}()
}

func (r *Runner) bashPPWait(ctx context.Context) {
	c := r.bashPPConcurrent
	if c == nil || r.bashPPGoTask {
		return
	}
	if ctx.Err() != nil {
		c.cancel()
	}
	c.mu.Lock()
	for c.active != 0 {
		c.changed.Wait()
	}
	c.quiesced = true
	var failure *bashPPTaskFailure
	if c.first != nil {
		copy := *c.first
		failure = &copy
	}
	c.mu.Unlock()
	if failure != nil {
		r.errf("bash++: task failed: %s\n", failure.text)
		if r.exit.code == 0 {
			r.exit.code = failure.code
		}
	}
	// A completed File Run never lends its quiesced registry to a later Run.
	// Handles left in persistent shell variables consequently become invalid
	// capabilities rather than aliases into stale channel state.
	r.bashPPConcurrent = nil
}

func (r *Runner) bashPPSelect(ctx context.Context, s *syntax.BashPPSelect) {
	var cases []reflect.SelectCase
	var arms []*syntax.BashPPSelectCase
	var def *syntax.BashPPSelectCase
	for _, arm := range s.Cases {
		if arm.Default {
			def = arm
			continue
		}
		switch comm := arm.Comm.(type) {
		case *syntax.BashPPReceive:
			c, ok := r.bashPPChannel(comm.Chan)
			if !ok {
				return
			}
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c.ch)})
		case *syntax.BashPPShortDecl:
			if comm.Recv == nil {
				r.errf("invalid select receive declaration\n")
				r.exit.code = 2
				return
			}
			c, ok := r.bashPPChannel(comm.Recv.Chan)
			if !ok {
				return
			}
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c.ch)})
		case *syntax.BashPPSend:
			c, ok := r.bashPPChannel(comm.Chan)
			if !ok {
				return
			}
			v := r.literal(comm.Value)
			if !r.bashPPValueFits(c.elem, v) {
				r.errf("bash++: cannot send %q as %s channel value\n", v, c.elem)
				r.exit.code = 2
				return
			}
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectSend, Chan: reflect.ValueOf(c.ch), Send: reflect.ValueOf(v)})
		default:
			r.errf("invalid select case\n")
			r.exit.code = 2
			return
		}
		arms = append(arms, arm)
	}
	if def != nil {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectDefault})
		arms = append(arms, def)
	}
	if def == nil {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(r.bashPPTaskContext(ctx).Done())})
	}
	if len(cases) == 0 {
		<-r.bashPPTaskContext(ctx).Done()
		r.exit.code = 1
		return
	}
	defer func() {
		if recover() != nil {
			r.errf("bash++: send on closed channel\n")
			r.exit.code = 2
		}
	}()
	i, v, open := reflect.Select(cases)
	if def == nil && i == len(arms) {
		r.exit.code = 1
		return
	}
	arm := arms[i]
	leave := r.bashPPPushScope()
	defer leave()
	if decl, yes := arm.Comm.(*syntax.BashPPShortDecl); yes {
		if len(decl.Lhs) == 0 || len(decl.Lhs) > 2 {
			r.errf("receive assignment mismatch\n")
			r.exit.code = 2
			return
		}
		text := ""
		if open {
			text = v.String()
		}
		r.bashPPDeclareName(decl.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: text})
		if len(decl.Lhs) == 2 {
			r.bashPPDeclareName(decl.Lhs[1].Value, expand.Variable{Set: true, Kind: expand.String, Str: strconv.FormatBool(open)})
		}
	}
	r.stmts(r.bashPPTaskContext(ctx), arm.Stmts)
}

func (r *Runner) bashPPRange(ctx context.Context, rng *syntax.BashPPRange) {
	c, ok := r.bashPPChannel(rng.Chan)
	if !ok {
		return
	}
	for {
		select {
		case v, open := <-c.ch:
			if !open {
				return
			}
			leave := r.bashPPPushScope()
			if len(rng.Names) == 1 {
				r.bashPPDeclareName(rng.Names[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: v})
			}
			r.cmd(r.bashPPTaskContext(ctx), rng.Body)
			leave()
			if r.exit.exiting || r.loopControlPending() {
				return
			}
		case <-r.bashPPTaskContext(ctx).Done():
			r.exit.code = 1
			return
		}
	}
}

func bashPPHasRuntimeHandle(value string) bool {
	return strings.HasPrefix(value, bashPPChanHandlePrefix)
}
