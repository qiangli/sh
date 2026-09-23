---
id: 8cceaeb5caa7
kind: enhancement
title: S250 reduce repeated native callback IPC wakeups for image
seq: 143
status: assigned
priority: p0
labels:
    - callback
    - tour
created: 2026-09-23T16:29:31.440949Z
weave: 239
assignee: qiangli
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Story #141 dependency. The unchanged original Go Tour solutions/image.go makes 131,078 interpreted original-method callbacks on the current bridge and misses the 60s Linux/Windows bound. Prototype a general cross-process callback mailbox or another measured transport that removes per-callback wakeups without skipping, caching, reordering, or compiling original callback bodies. Preserve interpreter-owned Runner serialization, nested/reentrant callback ownership, output-before-callback ordering, panic/unwind identity, cancellation/process death, receiver reference boundaries, authenticated session identity and native-fallback policy. Work only in an isolated sh branch. First collect an unchanged local control and outside-corpus focused semantics tests; then request a named DO2 slot for one authenticated original-source 60s image row. Do not change fixture/comparator/deadline or merge unmeasured experiments. Windows parity and final full Tour gates follow only after a Linux margin below about 45s is proven.

Local control and candidate on isolated `agent/weave-issue-239`, with the
unchanged authenticated image fixture (`d78cda6272212f8a73bd991efa502753b4ff36edcd004b21539c2111b16b547f`):
the original bridge passed in 13.11s test time; the bounded Unix shared-memory
mailbox passed in 9.49s (27.6% lower). The mailbox carries callback messages
that fit through atomically claimed slots; the authenticated socket still owns
startup/session lifetime, ordinary requests, oversized replies, process death,
and fallback. The current nested Runner frame alone may claim a slot, and an
in-flight lease defers unmapping across cancellation or nested `os.Exit`.
Outside-corpus output-order, nested/reentrant, concurrent, panic reuse/unwind,
cancellation/reset, stale-session and receiver-reference controls passed; the
focused concurrent/reentrant race gate and Windows fallback compile passed.
The first prototype needed a follow-up correction for ordinary request channel
ordering and oversized successful replies. The follow-up preserves the exact
result over the authenticated socket after one original body execution; a
40 KiB callback return with an exact-once side effect passed. A post-rebase
focused race run including the original image test passed in 74.605s test time,
and the Windows fallback cross-build passed.

DO2 authenticated one-row Tour evidence: candidate Bashy `8a68fab1` plus sh
`343a734c` (the reviewed mailbox on the then-current public sh base), original
source SHA-256 `d78cda6272212f8a73bd991efa502753b4ff36edcd004b21539c2111b16b547f`,
unchanged 60s step limit. Baseline, interpreted, and compiled observations all
passed; interpreted stage duration was **40,425 ms**, a 19,575 ms margin.
Candidate manifest SHA-256 `504d4465e87d1010fe932012fa176368923cdbd92f3807a26b0dc31734203179`,
ledger SHA-256 `a63ae80a1f185c169c7c00a13d2005208e12931705940b0d8729df2daf9a80b5`,
semantic root `d0342d5a46716f1724b5a375d2240967ba7c848a1d3cffabd1275dd27813cfdc`.
The `TOUR_ONLY=solutions/image.go` ledger is deliberately partial and reports
aggregate `3/291 FAIL`; it proves this row only. Public sh integration is
`3d0c5974`, and Windows parity is separately tracked by Story #147
(`6404343b27f2`). The final full Tour gate remains outstanding.
