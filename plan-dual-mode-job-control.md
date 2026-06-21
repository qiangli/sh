# Plan: dual-mode job control (goroutine default + opt-in real-process)

## Problem
Subshells / `cmd &` / pipelines run as goroutines, not `fork()` (Go has no usable
fork). Measured (Gate C, 2026-06-20): SCRIPTABLE job control is ~conformant
(`wait`/`wait $!`/`wait %n`, bg exit status, `jobs` listing, `kill %n`, `$!`,
`$$` — 11/12 vs bash 5.3), but INTERACTIVE terminal job control is non-functional:
no `[1] <pid>` notification, `fg`/`bg` return 1, no Ctrl-Z/monitor — because
goroutines have no PID, no process group, can't own the controlling terminal.

## Decision
Support BOTH, default to goroutine. Real-process job execution is an OPT-IN,
unix-only, additive path — so the fast pure-Go cross-platform default is
unchanged and the common case pays nothing. Build it only when VSC-PCTS data
shows interactive-JC assertions are load-bearing (measure first).

## Trigger
Prefer a DEDICATED control over overloading `--posix` (which is behavioral; bash's
own `--posix` never changes its process model). Options, composable:
- `set -m` (monitor mode) implies real-process jobs.
- An explicit `set -o realjobs` / `--realjobs` the conformance harness sets.
- Optionally let `--posix` imply it if that ergonomics is wanted.
Windows: real mode degrades to goroutine (no POSIX process groups).

## Components
1. Re-exec (not fork): a real-process job re-execs bashy on the sub-AST.
2. State serialization (the crux / main fidelity risk): non-exported vars,
   functions, aliases, shell options, traps, positional params, fd table →
   serialize (AST via existing `syntax/typedjson`; state via a temp fd/file) and
   rehydrate in the child. Anything unserialized diverges.
3. Unix JC machinery: setpgid / tcsetpgrp / forward SIGTSTP·SIGCONT·SIGINT to the
   foreground process group; reap via waitpid; map real PIDs into the jobs table.
4. Dual dispatch: the cmd/subshell/pipeline/background path branches on the mode
   flag; the re-exec path is additive behind the flag (default stays stable).

## Phasing
- P0 (done): Gate C measurement — scriptable conformant; interactive = ceiling.
- P1: minimal spike — `set -o realjobs; cmd &` re-execs a trivial (external-only)
   job with a real PID + correct `$!`/`wait`; prove the dispatch + reaping.
- P2: state serialization for shell-code jobs (vars/functions/options/traps) via
   typedjson + a state blob; subshell `( shellcode )` as a real process.
- P3: process groups + controlling terminal + signal forwarding → fg/bg/Ctrl-Z,
   `[1] <pid>` notifications, monitor-mode messages.
- P4: gate against VSC-PCTS interactive-JC assertions; document residual gaps.

## Caveats
Hard divergence from upstream mvdan/sh (keep it isolated behind the flag).
Unix-only real path. State-serialization fidelity must be tested exhaustively
(it's the part most likely to silently diverge). Keep the 86/0 bash suite +
posix-diff differential green throughout — the default (goroutine) path must not
regress.
