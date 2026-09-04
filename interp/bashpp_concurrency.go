// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

// The Bash++ concurrency runtime deliberately transports shell strings.  A
// channel is a typed, in-process capability; ordinary shell state remains a
// private Runner copy for every task.  This is both the useful boundary (tasks
// can communicate) and the safety boundary (a shell copy cannot accidentally
// share maps, traps, descriptors, or mutable variables).

import (
	"context"
	"reflect"
	"strconv"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

const bashPPChanHandlePrefix = "chan@bashpp:"

type bashPPChannel struct{ ch chan string }
type bashPPConcurrent struct {
	mu     sync.Mutex
	next   int
	chans  map[string]*bashPPChannel
	wg     sync.WaitGroup
	failed bool
	ioMu   sync.Mutex
}

func (r *Runner) bashPPConcurrency() *bashPPConcurrent {
	if r.bashPPConcurrent == nil {
		r.bashPPConcurrent = &bashPPConcurrent{chans: make(map[string]*bashPPChannel)}
	}
	return r.bashPPConcurrent
}

func (r *Runner) bashPPChannel(w *syntax.Word) (*bashPPChannel, bool) {
	name := r.literal(w)
	vr := r.lookupVar(name)
	h, ok := r.bashPPConcurrency().chans[vr.Str]
	if !ok {
		r.errf("Channel %s cannot cross a subshell boundary\n", name)
		r.exit.code = 2
	}
	if r.bashPPChanBoundary && ok {
		r.errf("Channel %s cannot cross a subshell boundary\n", name)
		r.exit.code = 2
		return nil, false
	}
	return h, ok
}

func (r *Runner) bashPPMakeChan(ctx context.Context, d *syntax.BashPPShortDecl) {
	if len(d.Lhs) != 1 {
		r.errf("assignment mismatch: make(chan) has one value\n")
		r.exit.code = 2
		return
	}
	cap := 0
	if d.MakeChan.Capacity != nil {
		var err error
		cap, err = strconv.Atoi(r.literal(d.MakeChan.Capacity))
		if err != nil || cap < 0 {
			r.errf("make(chan): invalid capacity\n")
			r.exit.code = 2
			return
		}
	}
	c := r.bashPPConcurrency()
	c.mu.Lock()
	h := bashPPChanHandlePrefix + strconv.Itoa(c.next)
	c.next++
	c.chans[h] = &bashPPChannel{ch: make(chan string, cap)}
	c.mu.Unlock()
	r.bashPPDeclareName(d.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: h})
}

func (r *Runner) bashPPSend(ctx context.Context, s *syntax.BashPPSend) {
	c, ok := r.bashPPChannel(s.Chan)
	if !ok {
		return
	}
	defer func() {
		if recover() != nil {
			r.errf("send on closed channel\n")
			r.exit.code = 2
		}
	}()
	select {
	case c.ch <- r.literal(s.Value):
	case <-ctx.Done():
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
	case <-ctx.Done():
		r.exit.code = 1
		return "", false
	}
	if lhs != nil {
		if len(lhs) > 2 || len(lhs) == 0 {
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
			r.errf("close of closed channel\n")
			r.exit.code = 2
		}
	}()
	close(c.ch)
}

func (r *Runner) bashPPGo(ctx context.Context, g *syntax.BashPPGo) {
	if g.Call == nil {
		return
	}
	c := r.bashPPConcurrency()
	c.wg.Add(1)
	child := r.subshell(true)
	child.bashPPConcurrent, child.bashPPGoTask, child.bashPPChanBoundary = c, true, false
	go func() {
		defer c.wg.Done()
		defer func() {
			if x := recover(); x != nil {
				child.errf("bash++ go panic: %v\n", x)
				c.mu.Lock()
				c.failed = true
				c.mu.Unlock()
			}
		}()
		child.bashPPCall(ctx, g.Call)
		if child.exit.code != 0 {
			c.mu.Lock()
			c.failed = true
			c.mu.Unlock()
		}
	}()
}

func (r *Runner) bashPPWait(ctx context.Context) {
	c := r.bashPPConcurrent
	if c == nil || r.bashPPGoTask {
		return
	}
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		r.exit.code = 1
		return
	}
	c.mu.Lock()
	failed := c.failed
	c.mu.Unlock()
	if failed && r.exit.code == 0 {
		r.exit.code = 1
	}
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
			arms = append(arms, arm)
		case *syntax.BashPPSend:
			c, ok := r.bashPPChannel(comm.Chan)
			if !ok {
				return
			}
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectSend, Chan: reflect.ValueOf(c.ch), Send: reflect.ValueOf(r.literal(comm.Value))})
			arms = append(arms, arm)
		default:
			r.errf("invalid select case\n")
			r.exit.code = 2
			return
		}
	}
	if def != nil {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectDefault})
		arms = append(arms, def)
	}
	if len(cases) == 0 {
		<-ctx.Done()
		return
	}
	i, v, ok := reflect.Select(cases)
	arm := arms[i]
	if recv, yes := arm.Comm.(*syntax.BashPPReceive); yes {
		_ = recv
		_ = v
		_ = ok
	}
	if decl, yes := arm.Comm.(*syntax.BashPPShortDecl); yes && decl.Recv != nil {
		r.bashPPDeclareName(decl.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: v.String()})
		if len(decl.Lhs) == 2 {
			r.bashPPDeclareName(decl.Lhs[1].Value, expand.Variable{Set: true, Kind: expand.String, Str: strconv.FormatBool(ok)})
		}
	}
	r.stmts(ctx, arm.Stmts)
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
			r.cmd(ctx, rng.Body)
			leave()
			if r.exit.exiting || r.loopControlPending() {
				return
			}
		case <-ctx.Done():
			r.exit.code = 1
			return
		}
	}
}
