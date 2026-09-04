// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"os"
	"reflect"
	"strconv"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

const (
	bashPPChanHandlePrefix = "chan@bashpp:"
	bashPPMaxChanCapacity  = 65536
)

type bashPPChannel struct {
	elem    string
	ch      chan string
	mu      sync.Mutex
	changed *sync.Cond
	closing chan struct{}
	closed  bool
	active  int
}

func newBashPPChannel(elem string, capacity int) *bashPPChannel {
	c := &bashPPChannel{elem: elem, ch: make(chan string, capacity), closing: make(chan struct{})}
	c.changed = sync.NewCond(&c.mu)
	return c
}

func (c *bashPPChannel) beginSend() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.active++
	return true
}

func (c *bashPPChannel) endSend() {
	c.mu.Lock()
	c.active--
	if c.active == 0 {
		c.changed.Broadcast()
	}
	c.mu.Unlock()
}

func (c *bashPPChannel) close() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.closed = true
	close(c.closing)
	for c.active != 0 {
		c.changed.Wait()
	}
	close(c.ch)
	return true
}

type bashPPObjectCloneKey struct {
	kind uint8
	ptr  uintptr
}

type bashPPObjectCloner struct {
	active map[bashPPObjectCloneKey]bool
	done   map[bashPPObjectCloneKey]any
}

func newBashPPObjectCloner() *bashPPObjectCloner {
	return &bashPPObjectCloner{
		active: make(map[bashPPObjectCloneKey]bool),
		done:   make(map[bashPPObjectCloneKey]any),
	}
}

func (c *bashPPObjectCloner) clone(value any) (any, error) {
	switch value := value.(type) {
	case nil, bool, string, float32, float64,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return value, nil
	case map[string]any:
		key := bashPPObjectCloneKey{kind: 1, ptr: reflect.ValueOf(value).Pointer()}
		if c.active[key] {
			return nil, fmt.Errorf("cyclic Bash++ object")
		}
		if done, ok := c.done[key]; ok {
			return done, nil
		}
		out := make(map[string]any, len(value))
		c.active[key] = true
		for name, item := range value {
			copy, err := c.clone(item)
			if err != nil {
				return nil, err
			}
			out[name] = copy
		}
		delete(c.active, key)
		c.done[key] = out
		return out, nil
	case []any:
		key := bashPPObjectCloneKey{kind: 2, ptr: reflect.ValueOf(value).Pointer()}
		if c.active[key] {
			return nil, fmt.Errorf("cyclic Bash++ object")
		}
		if done, ok := c.done[key]; ok {
			return done, nil
		}
		out := make([]any, len(value))
		c.active[key] = true
		for i, item := range value {
			copy, err := c.clone(item)
			if err != nil {
				return nil, err
			}
			out[i] = copy
		}
		delete(c.active, key)
		c.done[key] = out
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported mutable Bash++ object type %T", value)
	}
}

func cloneBashPPTaskVariable(vr expand.Variable, objects *bashPPObjectCloner) (expand.Variable, error) {
	vr = cloneBashPPVariable(vr)
	if vr.Kind != expand.Object || vr.Obj == nil {
		return vr, nil
	}
	copy, err := objects.clone(vr.Obj)
	if err != nil {
		return expand.Variable{}, err
	}
	vr.Obj = copy
	return vr, nil
}

func cloneBashPPTaskCells(r *Runner, objects *bashPPObjectCloner) error {
	seenScopes := make(map[*bashPPScope]bool)
	seenCells := make(map[*bashPPCell]bool)
	var visit func(*bashPPScope) error
	visit = func(scope *bashPPScope) error {
		if scope == nil || seenScopes[scope] {
			return nil
		}
		seenScopes[scope] = true
		if err := visit(scope.parent); err != nil {
			return err
		}
		for _, cell := range scope.entries {
			if seenCells[cell] {
				continue
			}
			seenCells[cell] = true
			copy, err := cloneBashPPTaskVariable(cell.vr, objects)
			if err != nil {
				return err
			}
			cell.vr = copy
		}
		return nil
	}
	if err := visit(r.bashPPScope); err != nil {
		return err
	}
	for _, scope := range r.bashPPFuncScopes {
		if err := visit(scope); err != nil {
			return err
		}
	}
	for _, fn := range r.bashPPFuncs {
		if err := visit(fn.scope); err != nil {
			return err
		}
	}
	for _, methods := range r.bashPPMethods {
		for _, fn := range methods {
			if err := visit(fn.scope); err != nil {
				return err
			}
		}
	}
	for _, fn := range r.bashPPClosures {
		if err := visit(fn.scope); err != nil {
			return err
		}
	}
	return nil
}

type bashPPTaskFailure struct {
	ordinal uint64
	code    uint8
	text    string
}

type bashPPTaskState struct {
	ordinal uint64
	ready   chan struct{}
	once    sync.Once
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
	failures []bashPPTaskFailure
	nextTask uint64
	tasks    map[uint64]*bashPPTaskState
	chans    map[string]*bashPPChannel
	ioMu     sync.Mutex
}

var bashPPConcurrencyInitMu sync.Mutex

func newBashPPConcurrent(parent context.Context) *bashPPConcurrent {
	ctx, cancel := context.WithCancel(parent)
	c := &bashPPConcurrent{ctx: ctx, cancel: cancel, chans: make(map[string]*bashPPChannel), tasks: make(map[uint64]*bashPPTaskState)}
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

func (c *bashPPConcurrent) add() (*bashPPTaskState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quiesced || c.ctx.Err() != nil {
		return nil, false
	}
	ordinal := c.nextTask
	c.nextTask++
	c.active++
	state := &bashPPTaskState{ordinal: ordinal, ready: make(chan struct{})}
	c.tasks[ordinal] = state
	return state, true
}

func (c *bashPPConcurrent) arm(task *bashPPTaskState) {
	if task == nil {
		return
	}
	task.once.Do(func() { close(task.ready) })
}

func (c *bashPPConcurrent) armed(task *bashPPTaskState) bool {
	if task == nil {
		return true
	}
	select {
	case <-task.ready:
		return true
	default:
		return false
	}
}

func (c *bashPPConcurrent) done(ordinal uint64, f *bashPPTaskFailure) {
	c.mu.Lock()
	state := c.tasks[ordinal]
	if f != nil {
		c.failures = append(c.failures, *f)
		// Failure is a structured-concurrency cancellation point, not something
		// deferred until the owner reaches EOF. The launch handshake makes this
		// deterministic: the owner cannot pass this task's `go` statement until
		// its first command has either completed or committed to a cancellable
		// block.
		c.cancel()
	}
	delete(c.tasks, ordinal)
	c.active--
	if c.active == 0 {
		c.changed.Broadcast()
	}
	c.mu.Unlock()
	// Always release a launcher, including snapshot failures, empty functions,
	// and panics before the first semantic command.
	c.arm(state)
}

func (c *bashPPConcurrent) stopAndJoin() *bashPPTaskFailure {
	c.cancel()
	c.mu.Lock()
	for c.active != 0 {
		c.changed.Wait()
	}
	c.quiesced = true
	result := c.primaryFailureLocked()
	c.mu.Unlock()
	return result
}

func (c *bashPPConcurrent) primaryFailureLocked() *bashPPTaskFailure {
	var result *bashPPTaskFailure
	for i := range c.failures {
		f := &c.failures[i]
		if result == nil || f.ordinal < result.ordinal {
			copy := *f
			result = &copy
		}
	}
	return result
}

func newBashPPChannelCapability() (string, error) {
	var raw [24]byte
	if _, err := cryptorand.Read(raw[:]); err != nil {
		return "", err
	}
	return bashPPChanHandlePrefix + hex.EncodeToString(raw[:]), nil
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
		// Wake any later operation in this malformed structured group; in
		// particular, an invalid send followed by a receive must not strand the
		// owner before it reaches the File join boundary.
		c.cancel()
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
	h, err := newBashPPChannelCapability()
	if err != nil {
		c.mu.Unlock()
		r.errf("make(chan): cannot allocate capability: %v\n", err)
		r.exit.code = 2
		return
	}
	c.chans[h] = newBashPPChannel(d.MakeChan.ChanType.Elem.Value, capacity)
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
	if r.bashPPConcurrent != nil {
		r.bashPPConcurrent.arm(r.bashPPTaskState)
	}
	if !c.beginSend() {
		r.errf("bash++: send on closed channel\n")
		r.exit.code = 2
		return
	}
	defer c.endSend()
	select {
	case c.ch <- v:
	case <-c.closing:
		r.errf("bash++: send on closed channel\n")
		r.exit.code = 2
	case <-r.bashPPTaskContext(ctx).Done():
		r.bashPPTaskCanceled = true
		r.exit.code = 1
	}
}

func (r *Runner) bashPPReceive(ctx context.Context, recv *syntax.BashPPReceive, lhs []*syntax.Lit) (string, bool) {
	c, ok := r.bashPPChannel(recv.Chan)
	if !ok {
		return "", false
	}
	if r.bashPPConcurrent != nil {
		r.bashPPConcurrent.arm(r.bashPPTaskState)
	}
	var v string
	var open bool
	select {
	case v, open = <-c.ch:
	case <-r.bashPPTaskContext(ctx).Done():
		r.bashPPTaskCanceled = true
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
	if !c.close() {
		r.errf("bash++: close of closed channel\n")
		r.exit.code = 2
	}
}

// bashPPTaskSnapshot is the explicit task boundary. subshell(true) clones
// variables, cwd, options, functions/types/imports, traps/signals/jobs and fd
// maps. The maps are task-owned; underlying OS open-file descriptions retain
// their normal shared offsets. Only the group and channel cores are shared.
func (r *Runner) bashPPTaskSnapshot(ordinal uint64) (*Runner, error) {
	child := r.subshell(true)
	child.bashPPConcurrent, child.bashPPGoTask, child.bashPPChanBoundary = r.bashPPConcurrent, true, false
	child.bashPPFileRun = true
	// A task is not a shell-copy boundary, so BASH_SUBSHELL stays unchanged.
	child.subshellLevel = r.subshellLevel
	// Tasks inherit every trap as a private snapshot, independent of the
	// errtrace/functrace inheritance rules used by actual shell subshells.
	child.trapCallbacks = cloneStringMap(r.trapCallbacks)
	child.inheritedExitTrap = false
	if child.trapCallbacks["ERR"] != "" {
		if child.noOpSetState == nil {
			child.noOpSetState = make(map[string]bool)
		}
		child.noOpSetState["errtrace"] = true
	}
	// Give deterministic tasks independent streams. Sharing PCG is both a data
	// race and schedule-dependent; the launch ordinal is stable by construction.
	if child.deterministic {
		seed := uint64(child.deterministicSeed) ^ (ordinal+1)*0x9e3779b97f4a7c15
		child.deterministicRng = mathrand.NewPCG(seed, seed^0xd1b54a32d192ed03)
	}
	child.randomSeed = r.randomSeed + uint32(ordinal+1)*0x9e37
	// Jobs and coprocs are owned by the task that creates them. An owner's
	// existing jobs are not waitable or mutable from a child task.
	child.bgProcs, child.lastBang, child.inheritedBang = nil, nil, nil
	child.doneBgPids = nil
	child.coprocReg, child.coprocFds, child.coprocReapedFds = nil, nil, nil
	child.coprocSeq = 0
	child.asyncList, child.asyncProc, child.jobsReadOnly = false, nil, false
	child.preferredJobID = 0
	child.exit = exitStatus{}
	child.expandRunExit = exitStatus{}
	child.bashPPDeferStack = nil
	child.bashPPReturn = bashPPReturnState{}
	child.bashPPPanic = bashPPPanicState{}
	child.bashPPDeferDepth = 0

	// The ordinary subshell copy aliases *os.File pointers. Tasks instead own
	// dup'd descriptors: aliases in the virtual fd tables stay aliases of one
	// duplicate, while the kernel open description (and therefore offset) is
	// shared with the owner. Closing a task descriptor cannot close the owner.
	dups := make(map[*os.File]*os.File)
	dup := func(f *os.File) (*os.File, error) {
		if f == nil {
			return nil, nil
		}
		if done := dups[f]; done != nil {
			return done, nil
		}
		copy, owned, err := dupPipeFd(f)
		if err != nil {
			return nil, err
		}
		if !owned && copy == f {
			return nil, fmt.Errorf("platform cannot duplicate task descriptor %s", f.Name())
		}
		dups[f] = copy
		if owned {
			child.bashPPTaskFiles = append(child.bashPPTaskFiles, copy)
		}
		return copy, nil
	}
	var err error
	if child.stdin, err = dup(r.stdin); err != nil {
		return child, err
	}
	if child.origStdin, err = dup(r.origStdin); err != nil {
		return child, err
	}
	if f, ok := r.stdout.(*os.File); ok {
		if child.stdout, err = dup(f); err != nil {
			return child, err
		}
	}
	if f, ok := r.origStdout.(*os.File); ok {
		if child.origStdout, err = dup(f); err != nil {
			return child, err
		}
	}
	if f, ok := r.stderr.(*os.File); ok {
		if child.stderr, err = dup(f); err != nil {
			return child, err
		}
	}
	if f, ok := r.origStderr.(*os.File); ok {
		if child.origStderr, err = dup(f); err != nil {
			return child, err
		}
	}
	for fd, f := range r.fdTable {
		if child.fdTable[fd], err = dup(f); err != nil {
			return child, err
		}
	}
	for fd, w := range r.fdWriteTable {
		if f, ok := w.(*os.File); ok {
			if child.fdWriteTable[fd], err = dup(f); err != nil {
				return child, err
			}
		}
	}
	// newOverlayEnviron's background snapshot is shallow for compatibility;
	// Bash++ tasks require deep mutable-value isolation.
	child.writeEnv = &overlayEnviron{}
	objects := newBashPPObjectCloner()
	for name, vr := range r.writeEnv.Each {
		copy, err := cloneBashPPTaskVariable(vr, objects)
		if err != nil {
			return child, fmt.Errorf("variable %s: %w", name, err)
		}
		_ = child.writeEnv.Set(name, copy)
	}
	if err := cloneBashPPTaskCells(child, objects); err != nil {
		return child, err
	}
	for name, typ := range child.bashPPTypes {
		typ.members = append([]string(nil), typ.members...)
		child.bashPPTypes[name] = typ
	}
	child.fillExpandConfig(r.ectx)
	return child, nil
}

func (r *Runner) closeBashPPTaskResources() {
	r.stopSignalSubscriptions()
	// Background jobs, coprocs, and process substitutions created by this task
	// inherit its group context. Cancel and join them before releasing their fd
	// owners; otherwise a File Run could return while task descendants still
	// use the cloned runner.
	for _, bg := range r.bgProcs {
		if bg != nil && bg.cancel != nil {
			bg.cancel()
		}
	}
	for _, bg := range r.bgProcs {
		if bg == nil {
			continue
		}
		<-bg.done
		if bg.coprocPid != 0 {
			r.reapCoproc(bg)
		}
	}
	r.bgProcs = nil
	for _, file := range r.bashPPTaskFiles {
		_ = file.Close()
	}
	r.bashPPTaskFiles = nil
	r.closeDirFile()
}

func cloneStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func (r *Runner) bashPPGo(ctx context.Context, g *syntax.BashPPGo) {
	if g.Call == nil {
		return
	}
	if !r.bashPPFileRun {
		r.errf("bash++: go requires an owning File Run\n")
		r.exit.code = 2
		return
	}
	c := r.bashPPConcurrency(ctx)
	state, ok := c.add()
	if !ok {
		// A prior child failure owns the eventual File status. A syntactically
		// later launch observes cancellation and has no side effects.
		return
	}
	ordinal := state.ordinal
	child, err := r.bashPPTaskSnapshot(ordinal)
	if err != nil {
		if child != nil {
			child.closeBashPPTaskResources()
		}
		c.done(ordinal, &bashPPTaskFailure{ordinal: ordinal, code: 2, text: fmt.Sprintf("task snapshot: %v", err)})
		return
	}
	child.bashPPTaskState = state
	go func() {
		var failure *bashPPTaskFailure
		defer func() {
			if x := recover(); x != nil {
				failure = &bashPPTaskFailure{ordinal: ordinal, code: 2, text: fmt.Sprintf("panic: %v", x)}
			}
			// A task is a terminal shell lifetime. Run its private EXIT snapshot
			// once even when group cancellation has already fired; cleanup traps
			// must not be skipped merely because a sibling failed.
			func() {
				defer func() {
					if x := recover(); x != nil && failure == nil {
						failure = &bashPPTaskFailure{ordinal: ordinal, code: 2, text: fmt.Sprintf("EXIT trap panic: %v", x)}
					}
				}()
				child.trapCallback(context.WithoutCancel(c.ctx), child.trapCallbacks["EXIT"], "exit")
			}()
			child.closeBashPPTaskResources()
			c.done(ordinal, failure)
		}()
		child.bashPPCall(c.ctx, g.Call)
		code := child.exit.code
		if child.bashPPTaskCanceled && child.bashPPTaskFailed {
			code = child.bashPPTaskFailCode
		}
		if code != 0 && child.trapCallbacks["ERR"] != "" {
			child.trapCallback(c.ctx, child.trapCallbacks["ERR"], "error")
		}
		canceled := !child.bashPPTaskFailed && (child.bashPPTaskCanceled || errors.Is(child.exit.err, context.Canceled) || errors.Is(child.exit.err, context.DeadlineExceeded))
		if code != 0 && !canceled {
			failure = &bashPPTaskFailure{ordinal: ordinal, code: code, text: fmt.Sprintf("exit status %d", code)}
		}
	}()
	// Deterministic launch handshake: a nonblocking first command completes
	// before the owner advances, while a known blocking operation announces
	// itself immediately before it blocks. This preserves useful concurrency
	// without allowing a fast failure to race later side effects.
	<-state.ready
}

func (r *Runner) bashPPWait(ctx context.Context) {
	c := r.bashPPConcurrent
	if c == nil || r.bashPPGoTask {
		return
	}
	// EOF is the structured lifetime boundary. Successful blocked tasks must
	// not keep a File Run alive forever.
	c.cancel()
	c.mu.Lock()
	for c.active != 0 {
		c.changed.Wait()
	}
	c.quiesced = true
	failure := c.primaryFailureLocked()
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
	var sends []*bashPPChannel
	closingCases := make(map[int]bool)
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
			if !c.beginSend() {
				for _, prior := range sends {
					prior.endSend()
				}
				r.errf("bash++: send on closed channel\n")
				r.exit.code = 2
				return
			}
			sends = append(sends, c)
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectSend, Chan: reflect.ValueOf(c.ch), Send: reflect.ValueOf(v)})
			arms = append(arms, arm)
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c.closing)})
			arms = append(arms, nil)
			closingCases[len(cases)-1] = true
			continue
		default:
			r.errf("invalid select case\n")
			r.exit.code = 2
			return
		}
		arms = append(arms, arm)
	}
	defer func() {
		for _, c := range sends {
			c.endSend()
		}
	}()
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
	if r.bashPPConcurrent != nil {
		r.bashPPConcurrent.arm(r.bashPPTaskState)
	}
	i, v, open := reflect.Select(cases)
	if closingCases[i] {
		r.errf("bash++: send on closed channel\n")
		r.exit.code = 2
		return
	}
	if def == nil && i == len(arms) {
		r.bashPPTaskCanceled = true
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
	if r.bashPPConcurrent != nil {
		r.bashPPConcurrent.arm(r.bashPPTaskState)
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
			r.bashPPTaskCanceled = true
			r.exit.code = 1
			return
		}
	}
}

func (r *Runner) bashPPHasRuntimeHandle(value string) bool {
	c := r.bashPPConcurrent
	if c == nil {
		return false
	}
	c.mu.Lock()
	_, ok := c.chans[value]
	c.mu.Unlock()
	return ok
}
