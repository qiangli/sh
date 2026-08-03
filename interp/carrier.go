// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package interp

import (
	"context"
	"fmt"
)

// A JobCarrier supplies kernel-visible stand-in processes ("carriers")
// for background jobs. This interpreter runs asynchronous lists
// (`cmd &`) as goroutines rather than forked shells, so a job made only
// of builtins or compound commands has no OS process of its own and
// `$!` falls back to the opaque "g<N>" handle. A host that needs strict
// POSIX process semantics — every `$!` a real, signalable kernel PID —
// opts in with [WithJobCarrier]; see that option for the semantics.
//
// Implementations should launch a process that blocks until killed or
// told to exit. Tying the carrier's stdin to a pipe held by the host is
// a good pattern: the carrier exits on EOF, so it cannot outlive a host
// that dies without cleaning up. Hosts that enable job control
// (`set -m`) should also place each carrier in its own process group,
// because group-directed signals target the negated PID.
type JobCarrier interface {
	// StartCarrier launches one carrier process. ctx is the context the
	// background job will run under; implementations may use it during
	// startup but should not tie the carrier's lifetime to it — the
	// runner reaps carriers explicitly via [CarrierProcess.Terminate].
	StartCarrier(ctx context.Context) (CarrierProcess, error)
}

// CarrierProcess is one live carrier obtained from [JobCarrier.StartCarrier].
type CarrierProcess interface {
	// Pid returns the carrier's kernel PID, which must be positive.
	Pid() int

	// Wait blocks until the carrier process has exited and been reaped,
	// returning the number of the signal that terminated it, or 0 if it
	// exited normally. The runner calls it exactly once, from a
	// goroutine it owns.
	Wait() int

	// Terminate makes the carrier exit promptly. It must be idempotent
	// and safe to call concurrently with Wait, including after the
	// carrier has already died.
	Terminate()
}

// WithJobCarrier gives background jobs a kernel-visible identity. For
// each asynchronous list (`cmd &`, including `a | b &`) the runner
// starts one carrier process via c and uses its real PID — instead of
// the synthetic "g<N>" handle — as the job's identity everywhere a PID
// is visible: `$!`, `jobs -p`/`jobs -l`, `wait <pid>`, and `wait -p`.
// External tools can then probe the job (`kill -0 $pid`) and signal it
// like any real shell child.
//
// The carrier is a signal proxy, not the job itself. A signal that
// terminates the carrier is relayed to the job according to the job's
// own disposition for that signal at that moment, like a signal
// arriving at a real shell child:
//
//   - Trapped (a non-empty `trap` action, set inside the async list or
//     inherited from the parent shell): the signal is queued into the
//     job's pending-signal machinery and the trap action runs at the
//     job's next statement boundary. The action decides the job's fate —
//     it may `exit`, or return and leave the job running. This works
//     even for a signal relayed before the job has started running.
//   - Ignored (`trap ” SIG`, a signal hard-ignored at shell startup, or
//     SIGINT/SIGQUIT's default ignore in an asynchronous list without
//     job control): the job keeps running, unaffected.
//   - Default — and always for the uncatchable SIGKILL and SIGSTOP —
//     the signal terminates the job: the runner cancels the job's
//     context (killing any external child it is currently running) and
//     the job's exit status becomes 128+signal (e.g. 143 for SIGTERM,
//     137 for SIGKILL), matching a real shell child.
//
// The carrier process itself always keeps its default dispositions:
// external `kill` decides the job's fate by killing the carrier, and
// the relay above happens when the runner reaps it. One consequence is
// that a job that survives a signal (trapped or ignored) has lost its
// carrier and with it its kernel identity — it can no longer be probed
// or signaled externally, though `wait` and `jobs` still resolve it.
// When the job finishes first, the runner reaps the carrier via
// [CarrierProcess.Terminate] and waits for its [CarrierProcess.Wait] to
// return before sealing the exit status, so a racing external kill
// either lands fully or is cleanly ignored, and by the time `wait`
// unblocks the carrier PID is truly gone — a following `kill -0 $!` sees
// no such process rather than a lingering carrier. PIDs of external
// processes the job spawns are still recorded, so `wait`/`kill` on those
// also resolve to the job.
//
// Opting in is a strict contract: the runner never falls back to a
// synthetic identity. If StartCarrier fails, or hands back a carrier
// with a nonpositive PID, the job is not started at all — the runner
// prints a diagnostic to stderr and the asynchronous statement fails
// with exit status 1, like a shell whose fork failed; `$!` keeps its
// previous value. Embedders that do not opt in keep the opaque "g<N>"
// handles and cannot claim strict process semantics for `$!`. Coprocs
// keep their existing synthetic `<NAME>_PID` and process substitutions
// are not jobs; neither uses a carrier. The option is ignored under
// `set -o dryrun` and in deterministic mode, which must not observe
// real PIDs.
//
// With a carrier configured, [WithBgPidCallback] fires once per
// background job with the carrier PID, rather than once per external
// process the job spawns.
func WithJobCarrier(c JobCarrier) RunnerOption {
	return func(r *Runner) error {
		r.jobCarrier = c
		return nil
	}
}

// attachCarrier gives bg a kernel-visible identity via the runner's
// JobCarrier, if any. Called from the parent goroutine after bg.cancel
// is set and before the job goroutine starts, so the identity is fixed
// from birth and `$!` never blocks on pidReady. job is the runner that
// will execute the asynchronous list; carrier signals are relayed into
// its trap machinery. A nil return with no carrier configured (or in
// dryrun/deterministic mode) keeps the legacy g<N> handle; a non-nil
// error means the job must not run at all — the host opted into real
// kernel identities, so there is no identity to degrade to.
func (r *Runner) attachCarrier(ctx context.Context, job *Runner, bg *bgProc) error {
	if r.jobCarrier == nil || r.dryRun || r.deterministic {
		return nil
	}
	cp, err := r.jobCarrier.StartCarrier(ctx)
	if err != nil {
		if cp != nil {
			go func() {
				cp.Terminate()
				cp.Wait()
			}()
		}
		return fmt.Errorf("job carrier: %v", err)
	}
	if cp == nil {
		return fmt.Errorf("job carrier: no carrier process")
	}
	pid := cp.Pid()
	if pid <= 0 {
		// A carrier without a usable PID is of no use; reap it and
		// report the failure.
		go func() {
			cp.Terminate()
			cp.Wait()
		}()
		return fmt.Errorf("job carrier: invalid carrier pid %d", pid)
	}
	bg.carrier = cp
	bg.carrierDone = make(chan struct{})
	// The carrier PID is the job's identity for its whole life: publish
	// it to `$!` now, and force publishPidToBang off so later exec PIDs
	// only accumulate in bg.pids without displacing it.
	bg.publishPidToBang = false
	bg.pid.Store(int64(pid))
	close(bg.pidReady)
	if bg.pidCallback != nil {
		bg.pidCallback(pid)
	}
	go func() {
		// Wait is the sole caller (per the CarrierProcess contract); its
		// return means the carrier has exited and been reaped. Signalling
		// carrierDone here is what lets reapCarrier guarantee the kernel PID
		// is gone before `wait` unblocks.
		sig := cp.Wait()
		defer close(bg.carrierDone)
		if bg.carrierReaped.Load() {
			return // the job finished first; reapCarrier tore this down
		}
		// The carrier died under a live job: relay the terminating
		// signal per the job's current disposition for it. A trapped
		// signal is queued so the job's own trap machinery runs the
		// action at its next statement boundary (safe even before the
		// job goroutine has started running); an ignored signal leaves
		// the job untouched; a default-disposition signal — or a
		// carrier that somehow exits normally, which must not outlive
		// its kernel identity — kills the job as 128+signal.
		if sig > 0 {
			switch name, disp := job.carrierSignalDisposition(sig); disp {
			case carrierSigTrapped:
				job.markPendingSignal(name)
				return
			case carrierSigIgnored:
				return
			}
			bg.killedSignal.CompareAndSwap(0, int32(sig))
		}
		bg.cancel()
	}()
	return nil
}

// carrierSigDisposition classifies how a job handles a signal relayed
// from its dead carrier: run a trap action, ignore it, or die.
type carrierSigDisposition int

const (
	carrierSigDefault carrierSigDisposition = iota
	carrierSigIgnored
	carrierSigTrapped
)

// carrierSignalDisposition reports the job runner's current disposition
// for the arrival of signal number num, and the signal's canonical
// name. Called from the carrier watcher goroutine while the job may be
// concurrently changing its traps, so the trapCallbacks read is guarded
// by sigMu (see setTrapCallback). Unknown signals — and SIGKILL and
// SIGSTOP, which are uncatchable no matter what `trap` recorded — are
// always default.
func (r *Runner) carrierSignalDisposition(num int) (string, carrierSigDisposition) {
	_, name, ok := signalByNumber(num)
	if !ok || name == "KILL" || name == "STOP" {
		return name, carrierSigDefault
	}
	if r.startupIgnored[name] {
		return name, carrierSigIgnored
	}
	r.sigMu.Lock()
	defer r.sigMu.Unlock()
	cb, ok := r.trapCallbacks[name]
	switch {
	case !ok:
		return name, carrierSigDefault
	case cb == "":
		return name, carrierSigIgnored
	}
	return name, carrierSigTrapped
}

// reapCarrier tears down the job's carrier process once the job itself
// has finished. Called from the job goroutine before the killedSignal
// read that seals the exit status, so an external kill racing with
// natural completion either lands as 128+signal or is cleanly ignored —
// never half-applied.
//
// It blocks until the carrier has been fully reaped (the watcher's
// [CarrierProcess.Wait] has returned), not merely asked to exit. That
// synchronization closes a lifecycle race: without it `wait` on a
// naturally-completed job could unblock while the carrier lingered as a
// zombie, so a following `kill -0 $!` still saw the PID and a retrying
// `wait` loop hit "pid is not a child". Terminate then Wait keeps the
// single-Wait contract — the watcher goroutine owns the only Wait call —
// while giving reapCarrier a happens-before edge on the reap.
func (bg *bgProc) reapCarrier() {
	if bg.carrier == nil {
		return
	}
	bg.carrierReaped.Store(true)
	bg.carrier.Terminate()
	<-bg.carrierDone
}
