// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

package interp

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// monitorActive reports whether job-control monitor mode (`set -m` / `set -o
// monitor`) is currently in effect. A non-interactive shell defaults to off;
// the state is tracked in noOpSetState because the runner doesn't otherwise
// model job control.
func (r *Runner) monitorActive() bool {
	return r.noOpSetState["monitor"]
}

// shellPid returns the PID this runner reports as $$, so the kill builtin can
// recognize a self-directed signal.
func (r *Runner) shellPid() int {
	if r.deterministic {
		return int(r.deterministicSeed & 0x7fff)
	}
	return os.Getpid()
}

// trapSignalActive reports whether this runner has installed an OS signal
// handler (via signal.Notify) for the named signal. A subshell clone returns
// false because the signal infrastructure is not inherited.
func (r *Runner) trapSignalActive(name string) bool {
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	_, ok := r.sigNotify[name]
	return ok
}

// enableSignalTrap ensures the OS signal named is delivered to this runner's
// pending-signal queue. Called by the `trap` builtin when a handler (or the
// empty "ignore" handler) is registered for a real signal. Pseudo-signals
// (EXIT/ERR/DEBUG/RETURN) are not OS signals and are ignored here.
func (r *Runner) enableSignalTrap(name string) {
	sig, ok := signalByName(name)
	if !ok {
		return
	}
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	if r.sigNotify == nil {
		r.sigNotify = make(map[string]os.Signal)
		r.pendingSig = make(map[string]int)
		r.sigCh = make(chan os.Signal, 32)
		r.sigWake = make(chan struct{}, 1)
		go r.signalLoop(r.sigCh)
	}
	if _, exists := r.sigNotify[name]; !exists {
		r.sigNotify[name] = sig
		signal.Notify(r.sigCh, sig)
	}
}

// disableSignalTrap stops OS delivery for the named signal, restoring its
// default disposition. Called by the `trap` builtin when a trap is reset.
func (r *Runner) disableSignalTrap(name string) {
	sig, ok := signalByName(name)
	if !ok {
		return
	}
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	if _, exists := r.sigNotify[name]; exists {
		delete(r.sigNotify, name)
		signal.Reset(sig)
	}
}

// signalLoop records every received OS signal in the pending queue and wakes
// any blocked wait. It runs until the channel is closed (never, in practice;
// the process exits first).
func (r *Runner) signalLoop(ch chan os.Signal) {
	for sig := range ch {
		r.sigMu.Lock()
		name := ""
		for n, s := range r.sigNotify {
			if s == sig {
				name = n
				break
			}
		}
		if name != "" {
			r.pendingSig[name]++
		}
		r.sigMu.Unlock()
		if name == "" {
			continue
		}
		r.hasPendingSig.Store(true)
		r.wakeSignalWaiters()
	}
}

// markPendingSignal queues a synchronously-delivered signal (a self-directed
// `kill` whose trap this runner owns), so the next statement boundary runs the
// handler without relying on OS signal delivery, which would race.
func (r *Runner) markPendingSignal(name string) {
	r.sigMu.Lock()
	if r.pendingSig == nil {
		r.pendingSig = make(map[string]int)
	}
	r.pendingSig[name]++
	r.sigMu.Unlock()
	r.hasPendingSig.Store(true)
	r.wakeSignalWaiters()
}

func (r *Runner) wakeSignalWaiters() {
	r.sigMu.Lock()
	wake := r.sigWake
	r.sigMu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

// nextPendingSignal pops the lowest-numbered pending signal name, or "" if
// none remain, and updates the fast-path flag.
func (r *Runner) nextPendingSignal() string {
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	best := ""
	bestNum := 1 << 30
	for n, c := range r.pendingSig {
		if c <= 0 {
			continue
		}
		num := 1 << 29
		if s, ok := signalByName(n); ok {
			num = int(s)
		}
		if num < bestNum {
			bestNum = num
			best = n
		}
	}
	if best != "" {
		r.pendingSig[best]--
	}
	any := false
	for _, c := range r.pendingSig {
		if c > 0 {
			any = true
			break
		}
	}
	if !any {
		r.hasPendingSig.Store(false)
	}
	return best
}

// deliverPendingSignals runs the trap handlers for any signals that have
// arrived since the last check. It is called at statement boundaries. The
// handlers' control flow (return/exit/break/continue) is allowed to propagate
// into the surrounding execution, matching bash's behaviour where a trap fired
// mid-function can unwind it.
func (r *Runner) deliverPendingSignals(ctx context.Context) {
	if r.handlingTrap || !r.hasPendingSig.Load() {
		return
	}
	for {
		name := r.nextPendingSignal()
		if name == "" {
			return
		}
		cb, ok := r.trapCallbacks[name]
		if !ok {
			continue // trap was reset after the signal arrived
		}
		r.runSignalTrap(ctx, cb, name)
		if r.exit.returning || r.exit.exiting || r.exit.fatalExit ||
			r.breakEnclosing > 0 || r.contnEnclosing > 0 {
			return
		}
	}
}

// runSignalTrap parses and runs a signal trap handler. Unlike
// [Runner.trapCallback] (used for EXIT/ERR/DEBUG/RETURN), it lets the handler's
// control flow propagate: a `return`, `exit`, `break`, or `continue` in the
// handler takes effect in the interrupted code. The pre-trap exit status is
// preserved when the handler completes without altering control flow.
func (r *Runner) runSignalTrap(ctx context.Context, callback, name string) {
	if callback == "" {
		return // explicitly-ignored signal (`trap '' SIG`)
	}
	wasHandling := r.handlingTrap
	r.handlingTrap = true
	defer func() { r.handlingTrap = wasHandling }()

	if s, ok := signalByName(name); ok {
		r.setVarString("BASH_TRAPSIG", strconv.Itoa(int(s)))
	}

	file, err := syntax.NewParser().Parse(strings.NewReader(callback), name+" trap")
	if err != nil {
		return // ignore parse errors in the callback, as trapCallback does
	}
	oldExit := r.exit
	r.exit = exitStatus{code: oldExit.code} // the handler sees the prior $?
	r.stmts(ctx, file.Stmts)
	if r.exit.returning || r.exit.exiting || r.exit.fatalExit ||
		r.breakEnclosing > 0 || r.contnEnclosing > 0 {
		return // let the handler's control flow unwind the interrupted code
	}
	r.exit = oldExit
}
