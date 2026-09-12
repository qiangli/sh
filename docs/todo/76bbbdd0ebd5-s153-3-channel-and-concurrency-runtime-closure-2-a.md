---
id: 76bbbdd0ebd5
kind: task
title: S153.3 channel and concurrency runtime closure (2 active roots + spike P's nonterminating set)
seq: 83
status: todo
priority: p0
created: 2026-09-10T21:18:21.129365Z
sprint: 153
---

Active roots (bashpp-tests/docs/upstream-harness/leaf-153r0/active-153-manifest.tsv (run 0 on candidate 4, partition v7, commit 2fa9eb5). The seed manifest is a canary only. Harness boundary unchanged: exact Sprint 157 upstream Go harness, Go/Bash only, unchanged inputs through Bash++ in both modes, no fallback, no timeout change, no corpus-specific branch.): chan/powser2.go (worker build failure — falls out of S153.4 class 1) and chan/sieve2.go ('bash++: task failed: exit status 1'), plus every deadline root spike P (interp/testdata/sprint153/deadline/FINDINGS.md) classifies NONTERMINATING rather than slow-correct (goroutine/channel completion, missed close/cancel). Outside-corpus pipeline covering send/receive, close/cancellation and goroutine completion, plus a leak/deadlock negative; deterministic reap, no surviving goroutine or process. Verify as S153.4 plus applicable -race tests.
