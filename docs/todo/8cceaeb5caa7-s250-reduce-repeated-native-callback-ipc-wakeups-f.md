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
mailbox passed in 9.49s (27.6% lower). The mailbox carries every callback and
reply in order through atomically claimed slots; the authenticated socket still
owns startup/session lifetime, ordinary and oversized requests, process death,
and fallback. The current nested Runner frame alone may claim a slot, and an
in-flight lease defers unmapping across cancellation or nested `os.Exit`.
Outside-corpus output-order, nested/reentrant, concurrent, panic reuse/unwind,
cancellation/reset, stale-session and receiver-reference controls passed; the
focused concurrent/reentrant race gate and Windows fallback compile passed.
This is local evidence only. One authenticated original-source DO2 image row at
the unchanged 60s deadline is required before integration or Windows timing.
