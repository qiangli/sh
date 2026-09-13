---
id: 5375c74adae1
kind: task
title: 'S6 compiled parity: settle the contradiction by test, not by editing the doc'
seq: 71
status: todo
priority: p1
created: 2026-09-10T10:45:21.559674Z
sprint: 146
---

sh/docs/lowering-agentic.md states that the statement and expression dispatcher in lower/compile.go does not thread frames yet, and that the groundwork therefore does not certify compiled agentic behaviour.

The code disagrees. lower/callables_methods.go:245 passes the marking through
programEntry, and lower/callables_shell.go:110 handles BashPPAgenticBlock.
Sprint 117 closed the lowering story 72cd8bec4ac6 on 2026-09-08 with its
lowering-parity goal checked.

One of the two is wrong and shipping either way is a v1.0.0 defect: a stale doc
that denies a working feature, or a checked goal over an unfinished one.

SETTLE IT BY RUNNING SOMETHING. Do not edit the doc to match the code or the
code to match the doc until a test says which is true. Required: the same
program in interpreted and compiled modes agrees on every element of the S1
contract - determinism when off, degradation and loud failure per S3, the
activation precedence of S2, and identical exit status and stream behaviour.

The compiled design is an immutable shellrt.Frame threaded explicitly, chosen
over a thread-local because tasks are ordinary goroutines and an ambient scope
would be racy and observable across regions the contract says are independent.
Verify that property directly: a frame copied into a goroutine must not observe
its parent's later transitions.

THEN correct whichever artifact was wrong, and say in the commit which it was.

Sprint: #146
