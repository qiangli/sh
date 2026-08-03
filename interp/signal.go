// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

package interp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"
)

var childSignalStartMu sync.Mutex

var errReadInterrupted = errors.New("read interrupted by signal")

// A SignalResetter restores a signal's real OS default disposition
// (SIG_DFL) when the shell resets it with `trap - SIG`.
//
// This interpreter is an embeddable library: by default it never returns
// a signal to a process-terminating default disposition, because a shell
// script must not be able to kill the process that hosts it. A signal the
// script ignores with an empty-action trap is set to a real SIG_IGN (so exec'd
// children inherit it), but `trap - SIG` only tears down the shell's own
// Go-level handler — it does not re-arm the terminating default. That is
// safe for embedders, but it diverges from POSIX: a standalone shell that
// ignores USR2 and then runs `trap - USR2` must again be killed by a later
// SIGUSR2 under its default disposition.
//
// A host that presents itself as a standalone POSIX shell — such as the
// `bashy` CLI — closes that gap by opting in with [WithSignalResetter].
// The runner then calls ResetDefault whenever `trap - SIG` resets a real
// signal the shell had made non-default (ignored via an empty action or trapped
// via a handler), asking the host to install SIG_DFL for it so a
// subsequently-delivered signal takes its default action. num is the
// platform signal number and name is its canonical (no-"SIG") name.
//
// [OSSignalResetter] is the ready-made implementation the standalone host
// wires in; embedders that never opt in keep the safe default and are
// never terminated by a signal their script reset.
type SignalResetter interface {
	ResetDefault(num int, name string)
}

// WithSignalResetter makes `trap - SIG` restore a real OS default signal
// disposition through s. Only a host that owns the whole process — a
// standalone POSIX shell — should opt in; see [SignalResetter] for why the
// default is to leave process-global dispositions untouched. The bashy CLI
// wires this as interp.WithSignalResetter(interp.OSSignalResetter{}).
func WithSignalResetter(s SignalResetter) RunnerOption {
	return func(r *Runner) error {
		r.sigReset = s
		return nil
	}
}

// OSSignalResetter is the standalone-host [SignalResetter]: it installs the
// process's real SIG_DFL for the reset signal at the OS level.
//
// os/signal cannot express this. signal.Ignore sets SIG_IGN, but a later
// signal.Reset does not undo it (once Ignore clears the runtime's
// "handling" bit, Reset is a no-op and the disposition stays SIG_IGN), and
// even for a trapped signal Reset only reinstalls Go's own runtime handler,
// which silently swallows an un-notified _SigNotify signal like SIGUSR2
// rather than taking its default terminating action. ResetDefault instead
// reuses restoreExecSignal, which on linux/darwin issues a raw rt_sigaction
// installing SIG_DFL directly; a delivered SIGUSR2 then terminates the
// process as POSIX requires. On other platforms restoreExecSignal falls
// back to signal.Reset, so the true default cannot be guaranteed there —
// acceptable, as the standalone bashy CLI targets linux and darwin.
type OSSignalResetter struct{}

// ResetDefault implements [SignalResetter].
func (OSSignalResetter) ResetDefault(num int, name string) {
	sig, ok := signalByName(name)
	if !ok {
		return
	}
	restoreExecSignal(signalForOS(sig))
}

type asyncExecSignal struct {
	name string
	sig  os.Signal
}

// monitorActive reports whether job-control monitor mode (`set -m` / `set -o
// monitor`) is currently in effect. A non-interactive shell defaults to off;
// the state is tracked in noOpSetState because the runner doesn't otherwise
// model job control.
func (r *Runner) monitorActive() bool {
	return r.noOpSetState["monitor"]
}

// ignoreAsyncListSignals applies POSIX 2.11 asynchronous-list signal
// defaults to a background runner when job control is disabled: SIGINT and
// SIGQUIT start ignored, but a trap command inside the async list may still
// replace or reset that disposition.
func (r *Runner) ignoreAsyncListSignals() {
	if r.monitorActive() {
		return
	}
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	if r.trapCallbacks == nil {
		r.trapCallbacks = make(map[string]string)
	}
	for _, sig := range [...]string{"INT", "QUIT"} {
		if _, ok := r.trapCallbacks[sig]; !ok {
			r.trapCallbacks[sig] = ""
		}
	}
}

// setTrapCallback and removeTrapCallback are the only mutation paths for
// trapCallbacks once a runner is executing. The map is otherwise confined
// to the runner's own goroutine, but a background job with a carrier (see
// carrier.go) has a watcher goroutine classifying relayed signals via
// carrierSignalDisposition, which reads the map under sigMu — so every
// concurrent-time write must hold sigMu too. Same-goroutine reads (trap
// delivery, `trap -p`, subshell cloning) stay lock-free.
func (r *Runner) setTrapCallback(name, callback string) {
	r.sigMu.Lock()
	if r.trapCallbacks == nil {
		r.trapCallbacks = make(map[string]string)
	}
	r.trapCallbacks[name] = callback
	r.sigMu.Unlock()
}

func (r *Runner) removeTrapCallback(name string) {
	r.sigMu.Lock()
	delete(r.trapCallbacks, name)
	r.sigMu.Unlock()
}

func (r *Runner) asyncIgnoredSignalsForExec(ctx context.Context) []asyncExecSignal {
	bg, _ := ctx.Value(bgProcCtxKey{}).(*bgProc)
	if bg == nil || bg.jobControl {
		return nil
	}
	var ignored []asyncExecSignal
	for _, name := range [...]string{"INT", "QUIT"} {
		if cb, ok := r.trapCallbacks[name]; !ok || cb != "" {
			continue
		}
		if sig, ok := signalByName(name); ok {
			ignored = append(ignored, asyncExecSignal{name: name, sig: signalForOS(sig)})
		}
	}
	return ignored
}

// startExecCmd starts cmd after applying POSIX asynchronous-list signal
// inheritance. Go exposes no per-child pre-exec signal-disposition hook, so the
// runner serializes this narrow fork/exec window and temporarily sets SIG_IGN
// for the signals that `cmd &` defaults introduced.
func (r *Runner) startExecCmd(ctx context.Context, cmd *exec.Cmd) error {
	ignored := r.asyncIgnoredSignalsForExec(ctx)
	if len(ignored) == 0 {
		return cmd.Start()
	}
	childSignalStartMu.Lock()
	defer childSignalStartMu.Unlock()
	for _, item := range ignored {
		signal.Ignore(item.sig)
	}
	err := cmd.Start()
	for _, item := range ignored {
		if r.startupIgnored[item.name] || r.sigIgnored[item.name] {
			signal.Ignore(item.sig)
		} else {
			restoreExecSignal(item.sig)
		}
	}
	return err
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

// owningSignalRunner returns the runner whose installed OS handler owns the
// named signal: this runner if it owns it, otherwise the nearest foreground
// ancestor (a self-directed `kill -SIG $$` inside a foreground subshell targets
// the parent shell's PID, so the parent's trap is the one that fires). Returns
// nil when no runner in the chain has an active handler, in which case the
// signal must be sent via the OS to take its default disposition.
func (r *Runner) owningSignalRunner(name string) *Runner {
	for cur := r; cur != nil; cur = cur.sigParent {
		if cur.trapSignalActive(name) {
			return cur
		}
	}
	return nil
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
	// SIGURG is used by the Go runtime for goroutine async-preemption. A
	// signal.Notify handler for it catches the runtime's OWN internal preemption
	// signals — they cannot be distinguished from an external SIGURG once
	// notified (a documented Go limitation) — which would fire the shell's trap
	// thousands of times per second and corrupt any harness that traps signal 23
	// (e.g. the VSC-PCTS TCM). A pure-Go shell therefore cannot deliver SIGURG
	// traps: record the trap (so `trap -p` shows it) but install no OS handler.
	if name == "URG" {
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
	_, hadNotify := r.sigNotify[name]
	_, hadIgnore := r.sigIgnored[name]
	if hadNotify || hadIgnore {
		delete(r.sigNotify, name)
		delete(r.sigIgnored, name)
		signal.Reset(signalForOS(sig))
	}
	sigReset := r.sigReset
	r.sigMu.Unlock()
	// signal.Reset above tears down the shell's Go-level handler but does
	// not, on its own, re-arm a terminating SIG_DFL after a `trap ''`
	// SIG_IGN. A standalone host opts in via WithSignalResetter to restore
	// the real OS default disposition, so a signal delivered after
	// `trap - SIG` again takes its default action (POSIX). Embedded hosts
	// leave sigReset nil and are never terminated by a reset they didn't
	// ask to re-arm. Run outside sigMu: the host callback touches process
	// signal state, not this runner's maps.
	if sigReset != nil && (hadNotify || hadIgnore) {
		if num, ok := signalNumber(sig); ok {
			sigReset.ResetDefault(num, name)
		}
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

func (r *Runner) signalWakeChan() <-chan struct{} {
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	return r.sigWake
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
	// The action is re-parsed and re-run on every delivery, so it must see
	// the alias definitions in effect now (when the trap fires), not be
	// suppressed by alias-timing against the freshly-parsed body's line 1.
	r.withAliasReparse(r.trapAliasReparseLine(), func() {
		r.stmts(ctx, file.Stmts)
	})
	if r.exit.returning || r.exit.exiting || r.exit.fatalExit ||
		r.breakEnclosing > 0 || r.contnEnclosing > 0 {
		return // let the handler's control flow unwind the interrupted code
	}
	r.exit = oldExit
}
