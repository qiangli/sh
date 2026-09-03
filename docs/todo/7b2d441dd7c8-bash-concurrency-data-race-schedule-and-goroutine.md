---
id: 7b2d441dd7c8
kind: task
title: Bash++ concurrency data-race, schedule, and goroutine-leak gate
seq: 2
status: todo
priority: p1
created: 2026-08-07T14:15:00Z
assignee: unassigned
sprint: 113
---

Before shipping the next Bash++ feature that adds shared-memory concurrency,
build a durable race and lifecycle test gate. The trigger is the TP714 lesson:
functional signal tests passed while their plain `bytes.Buffer` oracle had
concurrent reads and writes; `go test -race` correctly rejected them.

Required delivery:

1. Add a stable repository command/target that runs `go test -race` over every
   Bash++ parser, lowering, interpreter, channel, cancellation, signal, and
   transpiler package. Record Go version, OS/architecture, packages, and count.
2. Audit concurrency tests for unsafe buffers, maps, slices, shared runner state,
   polling, and unsynchronized flags. Replace them with mutex-protected
   observers, channels, atomics, or immutable snapshots and add a regression
   check that prevents reintroducing the unsafe pattern.
3. Test spawn/await, send/receive/close, timeout/cancellation, error and panic
   propagation, nested concurrency, multiple waiters, bounded fan-out,
   concurrent stdout/stderr, signals, and process-boundary serialization.
4. Exercise adversarial orderings: close-vs-send, cancel-vs-complete,
   parent-exit-vs-child, signal-vs-blocked-read, and simultaneous completion.
   Run focused cases repeatedly and with varied `GOMAXPROCS` values.
5. Detect leaked goroutines, pipes, child processes, timers, and blocked channel
   operations. Every timeout must fail with useful diagnostics rather than hang.
6. Where interpreted and transpiled/native Bash++ paths both exist, run the same
   behavioral corpus against both. Preserve Bash++-off Bash compatibility gates.
7. Integrate the future concurrency tranche from the Go example corpus only
   after this gate is green. Quarantines must name an owner, rationale, expiry,
   and must never convert a race report into a pass.

Acceptance: all focused functional tests pass repeatedly, all relevant packages
pass `go test -race`, leak checks are clean, no test oracle itself races, and CI
publishes the evidence without network dependence or zero-test success.
