# S221.4 — B13/B14: one bounded process line substrate

Sprint: #221 · Story: #575 · Story-ID: 0d0787f57ca1

## Goal

Ship the dialect's live-process line iteration over **one** internal, bounded
Go-channel process primitive:

- **B13** `run(...).Lines()` — split a *completed* capture's stdout into lines.
- **the substrate** — one bounded-channel process line reader that both B14 and
  the downstream streaming adapters (B5/B7) consume.
- **B14** `start(...)` — a live process handle with a **cancellable** `Lines()`
  and an **exact-once** `Wait()`.

## Design of record

The substrate (`interp/bashpp_process.go`, `bashPPLineProcess`) settles the hard
parts once:

- **Producer owns close.** Exactly one goroutine writes the lines channel and is
  its only closer (on stdout EOF or cancellation). Consumers never close it, so
  `range Lines()` terminates cleanly with no send-on-closed race.
- **Errors stay separate from data.** A line is a line; scan/IO failures and
  cancellation surface through `Wait`'s *error*, never as a data line. The
  process's own exit status (non-zero code or signal) is a *status* — the first
  result of `Wait` — matching bash's `$?`, not an error.
- **Every process is drained and reaped.** stdout and stderr are both drained to
  completion even when the consumer stops early, so the child never blocks on a
  full pipe; the process is `Wait`'d exactly once (`sync.Once`) so it is reaped,
  not left a zombie.
- **Bounded channel.** Capacity is fixed at construction → a slow consumer
  backpressures the producer rather than growing an unbounded queue.
- **No blocking properties.** `Lines()` just returns the channel; `Stderr()`
  returns already-drained bytes. `Wait` is the sole blocking call.

`bashPPProcessSource` abstracts "a started process" (`Stdout`/`Stderr`/`Wait`/
`Kill`) so the same substrate serves a real `os/exec` child
(`bashPPCmdSource`, `interp/bashpp_process_cmd.go`) and the in-process subshell
that the `start(...)` surface will use. Signal/status mapping is the platform
split `bashPPProcessExitStatus` (`interp/bashpp_native_process_{unix,other}.go`),
using bash's 128+N signal convention on unix.

Kill-vs-reap safety: `bashPPCmdSource` serialises `Kill` against `Wait`'s reaped
transition with a mutex and only group-kills while un-reaped — a pid is not
reused before it is reaped, so the group signal can never land on an unrelated
process. The substrate cancels its watcher goroutine after reaping so a normal
completion leaks nothing (`TestBashPPProcessNoGoroutineLeak`).

## Test provenance (faithful ports; original assertions preserved)

Recorded in full in the header of `interp/bashpp_process_test.go`:

- **Go standard library `os/exec`** — pinned **go1.27.1** (`$GOROOT` sdk
  go1.27.1), `src/os/exec/exec_test.go`, **BSD-3-Clause** (`$GOROOT/LICENSE`,
  "Copyright (c) 2009 The Go Authors").
  - `TestExitStatus`/`TestExitCode` → `TestBashPPProcessExitStatusFidelity`
    (exit-code passthrough).
  - `TestContext`/`TestContextCancel` → `TestBashPPProcessContextCancel[Fake]`
    (cancellation kills; `Wait` then errors; the process does not outlive its
    context).
  - the double-`Wait`/reaping invariant → the exact-once `Wait` tests.
  - Adaptation: upstream drives a compiled `exec` helper binary via
    `helperCommand`; we cannot ship it, so we drive the host `sh` with the same
    observable behaviours (exit N, sleep, two-stream output). Assertions kept.
- **GNU Bash** (Bash 5.3, the repo's `TestRunnerRunConfirm` oracle) — Bash
  Reference Manual, "Exit Status": a fatal signal N yields 128+N; `wait`
  returns the awaited process's status.
  - → `TestBashPPProcessSignalStatus` (SIGTERM'd child reports 143) and
    exit-code passthrough. Adaptation: expressed against
    `bashPPProcessExitStatus`/`Wait` rather than a `$?` string, since this is a
    Go-level unit.

## Coverage matrix (story-required scenarios → tests)

| scenario | test |
| --- | --- |
| empty / final unterminated / empty middle lines | `TestBashPPProcessLineSemantics` |
| large stdout **and** stderr, drained | `TestBashPPProcessLargeStdoutStderr` |
| bounded backpressure | `TestBashPPProcessBoundedBackpressure` |
| early close reaps, producer sole closer | `TestBashPPProcessEarlyCloseReaps` |
| cancellation | `TestBashPPProcessContextCancel`, `...ContextCancelFake` |
| exact-once wait | `TestBashPPProcessExactOnceWait`, `...RealDoubleWait` |
| signal exit status (128+N) | `TestBashPPProcessSignalStatus` |
| leaks / reaping (unix + windows) | `TestBashPPProcessNoGoroutineLeak`; the
  in-memory fakes run cross-platform, the real-process ports skip where no `sh`
  (Windows CI) and rely on the fakes there |

## Surface status

The substrate is complete and the dialect spellings are wired on top of it
(`interp/bashpp_process_surface.go`), through the narrowest seams the story
permits — no general method evaluator, no second streaming abstraction:

- **Parser** (`syntax/bashpp_concurrency.go`, `bashppRangeLinesCall`): a range
  admits exactly one chained call, `x.Lines()` (two-part selector, no
  arguments), recorded as `BashPPRange.Call`; `Chan` keeps the spelled text so
  the printer round-trips and `Walk` needs no new case. `p, err := start(...)`
  and `status, err := p.Wait()` / `p.Close()` already parse as `BashPPCall`s.
- **B13** `for line := range r.Lines()` over a completed `run(...)` result
  splits the buffered `Stdout` with the same scanner boundaries as the live
  substrate (`bashPPSplitLines`): final unterminated line is a line, empty
  middle lines are lines, a trailing newline adds nothing. It never streams.
- **B14** `p, err := start(...)` launches argv in an in-process subshell as a
  `bashPPProcessSource` (`bashPPSubshellSource`: stdout piped to the substrate,
  stderr passed straight through, cancel = kill, done = wait) and binds an
  opaque handle Object. `range p.Lines()` receives from the bounded channel and
  is cancellable by the runner's context (cancel → kill, drain, reap; `Wait`
  then reports the cancellation). `status, err := p.Wait()` is exact-once and
  the sole blocking call. `p.Close()` abandons early (kill, drain, reap) and is
  a no-op after `Wait`. Both dispatch ahead of the selector lookup, only when
  the base name is a live handle.
- **Lowering**: `run`/`capture`/`start` are the interpreter's process boundary
  and are not lowered; a `range x.Lines()` reports itself interpreter-only
  rather than emitting Go that could not compile (`lower/callables.go`).

Acceptance tests for the literal spellings (`interp/bashpp_process_surface_test.go`,
`TestBashPPRunLines*` / `TestBashPPStart*`) carry the same os/exec go1.27.1
BSD-3-Clause and Bash 5.3 exit-status provenance as the substrate tests, and
cover: empty / final unterminated / empty middle lines, non-zero status,
cancellation, early close + reap, large stdout **and** stderr, exact-once
wait, signal status (143), diagnostics, printer round-trip, and no goroutine
leak.
