# Plan: opt-in OS-backed job-carrier seam for background jobs

## Problem

Asynchronous lists (`cmd &`) run as goroutines, not forked shells. Jobs that
never `exec` a real process (pure builtins, compound commands) have no kernel
PID; `$!` falls back to the synthetic `g<N>` handle, which external tools
cannot probe (`kill -0`) or signal. A host that presents itself as a strict
POSIX `sh` (bashy) needs a real, kernel-visible child PID per job — without
fabricating numeric PIDs and without coupling this library to the bashy binary.

## Design

New file `interp/carrier.go`, fully portable (no syscalls in the seam):

- `type JobCarrier interface { StartCarrier(ctx) (CarrierProcess, error) }`
- `type CarrierProcess interface { Pid() int; Wait() int; Terminate() }`
  - `Wait` blocks until the carrier process exits and returns the number of
    the signal that terminated it (0 for a normal exit). Called exactly once.
  - `Terminate` must be idempotent and safe concurrently with `Wait`.
- `WithJobCarrier(c JobCarrier) RunnerOption` stores it on the Runner;
  copied across `Reset` and into subshells like `bgPidCallback`.

Wiring (in `Runner.stmt`'s background branch):

- After `bg.cancel` is set, `r.attachCarrier(bgCtx, bg)` starts one carrier
  per job. On success the carrier PID becomes the job's identity from birth:
  `bg.pid` is stored, `bg.pidReady` closed, `bg.publishPidToBang` forced
  false so later exec PIDs land only in `bg.pids` (still resolvable by
  `wait`/`kill`) and never displace the identity. `$!`, `jobs -p/-l`,
  `wait <pid>`, `wait -p`, `doneBgPids` retention, and the numeric `kill`
  paths all read `bg.pid`, so no changes are needed there.
- A watcher goroutine calls `Wait()`. If the carrier dies while the job is
  live (external TERM/KILL/HUP/…), it CASes `bg.killedSignal` to the signal
  and cancels the job's context — killing any current external child — so
  the job's status becomes 128+signal (143/137), exactly like the existing
  `killSyntheticBg` path.
- When the job goroutine finishes, `bg.reapCarrier()` sets an atomic
  `carrierReaped` flag and calls `Terminate()`; the watcher sees the flag
  and stands down. Reap happens before the `killedSignal` read so a racing
  external kill still lands.
- On `StartCarrier` error the job silently degrades to the legacy `g<N>`
  handle (generic embedders keep today's behavior; they cannot claim strict
  process semantics). The option is skipped in dryrun/deterministic modes.
- Scope: `&` jobs only (including `a | b &`). Coprocs keep their existing
  synthetic `<NAME>_PID`; process substitutions are not jobs.

Explicitly rejected: minting synthetic numeric PIDs from shellPid+counter
(run #1) — every numeric identity handed out is a real kernel PID.

## Tests

Test carrier re-execs the test binary (`GOSH_CMD=carrier` helper in
`TestMain`) which blocks reading stdin until EOF — so carriers also die if
the test binary exits. `Terminate` closes stdin and kills; `Wait` maps the
exit to a signal number via a `//go:build unix` helper (0 on other GOOS).

- portable (`carrier_test.go`): identity across `$!`/`jobs -p`/`wait -p`;
  rapid uniqueness; status retention (`wait; wait $p` → saved status, then
  127); subshell isolation (inherited `$!` visible, `wait` refused);
  Reset; custom ExecHandlers; StartCarrier failure fallback; no-carrier
  runners keep `g<N>`.
- unix (`carrier_unix_test.go`): `/bin/kill -0 $!` sees a live PID;
  external TERM → 143 and KILL → 137 for pure-builtin loops and for jobs
  with an external child (child killed promptly); concurrent jobs' PIDs
  unique; kill-storm race test; ctx cancellation reaps the carrier
  (polled via `kill(pid, 0)` → ESRCH).
