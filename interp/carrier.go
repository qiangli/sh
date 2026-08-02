// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package interp

import "context"

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
// terminates the carrier is relayed to the job: the runner cancels the
// job's context — killing any external child it is currently running —
// and the job's exit status becomes 128+signal (e.g. 143 for SIGTERM,
// 137 for SIGKILL), matching a real shell child. When the job finishes
// first, the runner reaps the carrier via [CarrierProcess.Terminate].
// PIDs of external processes the job spawns are still recorded, so
// `wait`/`kill` on those also resolve to the job.
//
// If StartCarrier fails, the job silently degrades to the legacy "g<N>"
// handle — the behavior runners without this option always have.
// Embedders that do not opt in keep those opaque handles and cannot
// claim strict process semantics for `$!`. Coprocs keep their existing
// synthetic `<NAME>_PID` and process substitutions are not jobs; neither
// uses a carrier. The option is ignored under `set -o dryrun` and in
// deterministic mode, which must not observe real PIDs.
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
// from birth and `$!` never blocks on pidReady. No-op (leaving the
// legacy g<N> handle) when no carrier is configured or starting one
// fails.
func (r *Runner) attachCarrier(ctx context.Context, bg *bgProc) {
	if r.jobCarrier == nil || r.dryRun || r.deterministic {
		return
	}
	cp, err := r.jobCarrier.StartCarrier(ctx)
	if err != nil || cp == nil {
		return
	}
	pid := cp.Pid()
	if pid <= 0 {
		// A carrier without a usable PID is of no use; reap it and
		// degrade to the synthetic handle.
		go func() {
			cp.Terminate()
			cp.Wait()
		}()
		return
	}
	bg.carrier = cp
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
		sig := cp.Wait()
		if bg.carrierReaped.Load() {
			return // the job finished first; reapCarrier tore this down
		}
		// The carrier died under a live job: relay the terminating
		// signal as 128+sig and cancel the job, killing any external
		// child it is currently running. A carrier that somehow exits
		// normally still cancels the job — it must not outlive its
		// kernel identity.
		if sig > 0 {
			bg.killedSignal.CompareAndSwap(0, int32(sig))
		}
		bg.cancel()
	}()
}

// reapCarrier tears down the job's carrier process once the job itself
// has finished. Called from the job goroutine before the killedSignal
// read that seals the exit status, so an external kill racing with
// natural completion either lands as 128+signal or is cleanly ignored —
// never half-applied.
func (bg *bgProc) reapCarrier() {
	if bg.carrier == nil {
		return
	}
	bg.carrierReaped.Store(true)
	bg.carrier.Terminate()
}
