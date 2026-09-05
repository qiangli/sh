---
id: f26704e7c4b9
kind: task
title: 'Race gate: process-directed SIGPIPE is undeliverable under TSan'
seq: 18
status: todo
priority: p0
created: 2026-09-05T00:22:24.946171Z
sprint: 115
---

TestPipelineBuiltinSIGPIPEIsolation lost a same-process SIGPIPE 2 of 3 under GOMAXPROCS=1 -race with the full focused lane. Measured resolution, not inference: Linux delivers a process-directed signal to the main thread when that thread does not block it; the Go runtime's m0 blocks SIGUSR1 but not SIGPIPE. Under -race, m0 is parked in the runtime rather than instrumented code, and TSan defers a signal arriving there until the thread reaches an interceptor, which never happens. Evidence: kill(getpid,SIGPIPE) leaves nothing pending in the kernel and 20 consecutive retries are ALL lost; tgkill to the running thread is delivered every time; process-directed SIGUSR1, which m0 blocks so it lands elsewhere, is delivered every time. A minimal Go program reproducing the same syscall dance loses nothing in 100 rounds with or without -race, so this is not the shell. Fix: thread-directed delivery for that stage under -race, process-directed kept otherwise; the shell-visible guarantee stays covered end-to-end by the semantics helper.
