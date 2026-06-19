// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

package interp

import (
	"context"
	"os"
	"os/signal"
	"sort"
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

// parseHardIgnore decodes the comma-separated signal-name list carried in
// BashyHardIgnoreEnv into a set of canonical (no-SIG-prefix) names. Unknown or
// empty entries are dropped. Returns nil when there is nothing to ignore.
func parseHardIgnore(s string) map[string]bool {
	if s == "" {
		return nil
	}
	var set map[string]bool
	for _, name := range strings.Split(s, ",") {
		sig := normalizeSignal(name)
		if sig == "" {
			continue
		}
		if _, ok := signalByName(sig); !ok {
			continue // pseudo-signals can't be hard-ignored
		}
		if set == nil {
			set = make(map[string]bool)
		}
		set[sig] = true
	}
	return set
}

// hardIgnoreEnvValue serialises the signals this runner has set to real
// SIG_IGN for the BashyHardIgnoreEnv bridge, so a child shell we exec treats
// them as ignored-on-entry. Returns "" when none are ignored.
func (r *Runner) hardIgnoreEnvValue() string {
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	if len(r.sigIgnored) == 0 {
		return ""
	}
	names := make([]string, 0, len(r.sigIgnored))
	for name := range r.sigIgnored {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// isStartupIgnored reports whether the named real signal was SIG_IGN when this
// process started. Bash flags such signals as SIG_HARD_IGNORE: a `trap` that
// tries to set or reset them is a silent no-op, and they are listed with an
// empty action. Pseudo-signals are never startup ignored.
func (r *Runner) isStartupIgnored(name string) bool {
	if _, ok := signalByName(name); !ok {
		return false
	}
	return r.startupIgnored[name]
}

// notifyChildReaped queues one SIGCHLD trap run for a child that has just been
// reaped, matching bash's run_sigchld_trap-per-reaped-child in waitchld
// (jobs.c): each reaped child runs the trap once, fired at the next statement
// boundary. A no-op when no SIGCHLD trap is active. Driving CHLD from reaps
// rather than from the OS signal is what makes it deterministic — the kernel
// coalesces simultaneous child deaths into a single SIGCHLD, losing traps.
func (r *Runner) notifyChildReaped() {
	if !r.chldTrapActive.Load() {
		return
	}
	r.markPendingSignal("CHLD")
}

// enableSignalTrap ensures the OS signal named is delivered to this runner's
// pending-signal queue. Called by the `trap` builtin when a non-empty handler
// is registered for a real signal. Pseudo-signals (EXIT/ERR/DEBUG/RETURN) are
// not OS signals and are ignored here.
func (r *Runner) enableSignalTrap(name string) {
	// SIGCHLD is driven from child reaps (notifyChildReaped), not from OS
	// signal delivery, so it never needs an OS handler. Installing one would
	// also risk perturbing Go's own child reaping.
	if name == "CHLD" {
		return
	}
	sig, ok := signalByName(name)
	if !ok {
		return
	}
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	delete(r.sigIgnored, name)
	if r.sigNotify == nil {
		r.sigNotify = make(map[string]os.Signal)
		r.pendingSig = make(map[string]int)
		r.sigCh = make(chan os.Signal, 32)
		r.sigWake = make(chan struct{}, 1)
		go r.signalLoop(r.sigCh)
	}
	if _, exists := r.sigNotify[name]; !exists {
		osSig := signalForOS(sig)
		r.sigNotify[name] = osSig
		signal.Notify(r.sigCh, osSig)
	}
}

// ignoreSignalTrap sets a real OS SIG_IGN for the named signal. Unlike a
// notified trap (which installs a Go handler and is therefore reset to SIG_DFL
// across execve), a real SIG_IGN is inherited as SIG_IGN by exec'd children,
// which is exactly how bash makes an ignored signal hard-ignored in a child
// shell (trap.tests/trap1.sub). Pseudo-signals are ignored here; the empty
// trapCallbacks entry alone records their state.
func (r *Runner) ignoreSignalTrap(name string) {
	// SIGCHLD is reap-driven; never touch its OS disposition. A real SIG_IGN
	// on SIGCHLD would also break child reaping (auto-reaped children make
	// wait() fail with ECHILD on some systems).
	if name == "CHLD" {
		return
	}
	sig, ok := signalByName(name)
	if !ok {
		return
	}
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	delete(r.sigNotify, name)
	if r.sigIgnored == nil {
		r.sigIgnored = make(map[string]bool)
	}
	r.sigIgnored[name] = true
	signal.Ignore(signalForOS(sig)) // also undoes any prior signal.Notify for sig
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
	_, hadNotify := r.sigNotify[name]
	_, hadIgnore := r.sigIgnored[name]
	if hadNotify || hadIgnore {
		delete(r.sigNotify, name)
		delete(r.sigIgnored, name)
		signal.Reset(signalForOS(sig))
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
			if n, ok := signalNumber(s); ok {
				num = n
			}
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

// peekPendingSignal returns the lowest-numbered pending signal's name and
// number without consuming it, or ("", 0) if none are pending.
func (r *Runner) peekPendingSignal() (string, int) {
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
			if n, ok := signalNumber(s); ok {
				num = n
			}
		}
		if num < bestNum {
			bestNum = num
			best = n
		}
	}
	if best == "" {
		return "", 0
	}
	return best, bestNum
}

// waitOrSignal blocks until bg finishes or a trapped signal arrives, returning
// true if a signal interrupted the wait. POSIX requires `wait` to return
// immediately (with status >128) when a trapped signal is received; the trap
// itself runs at the next statement boundary via deliverPendingSignals.
func (r *Runner) waitOrSignal(bg *bgProc) (interrupted bool) {
	r.sigMu.Lock()
	wake := r.sigWake
	r.sigMu.Unlock()
	if wake == nil {
		<-bg.done
		return false
	}
	for {
		select {
		case <-bg.done:
			return false
		case <-wake:
			// SIGCHLD does not interrupt wait — bash runs its trap per
			// child death but keeps waiting. The pending CHLD trap fires
			// at the next statement boundary. Any other trapped signal
			// returns immediately (POSIX).
			if name, _ := r.peekPendingSignal(); name != "" && name != "CHLD" {
				return true
			}
			// CHLD or spurious wake; keep waiting
		}
	}
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
		if n, ok := signalNumber(s); ok {
			r.setVarString("BASH_TRAPSIG", strconv.Itoa(n))
		}
	}

	file, err := syntax.NewParser().Parse(strings.NewReader(callback), name+" trap")
	if err != nil {
		return // ignore parse errors in the callback, as trapCallback does
	}
	oldExit := r.exit
	// POSIX interp 1602: a bare `return` directly in this action returns
	// the $? in effect at trap entry. Record it and the call depth so the
	// return builtin can tell action-level from nested-function returns.
	oldInSig, oldDepth, oldSaved := r.inSignalTrap, r.signalTrapDepth, r.signalTrapExit
	r.inSignalTrap = true
	r.signalTrapDepth = len(r.callStack)
	r.signalTrapExit = oldExit.code
	defer func() {
		r.inSignalTrap, r.signalTrapDepth, r.signalTrapExit = oldInSig, oldDepth, oldSaved
	}()
	r.exit = exitStatus{code: oldExit.code} // the handler sees the prior $?
	r.stmts(ctx, file.Stmts)
	if r.exit.returning || r.exit.exiting || r.exit.fatalExit ||
		r.breakEnclosing > 0 || r.contnEnclosing > 0 {
		return // let the handler's control flow unwind the interrupted code
	}
	r.exit = oldExit
}
