---
id: 8cceaeb5caa7
kind: enhancement
title: S250 reduce repeated native callback IPC wakeups for image
seq: 143
status: todo
priority: p0
labels:
    - callback
    - tour
created: 2026-09-23T16:29:31.440949Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Story #141 dependency. The unchanged original Go Tour solutions/image.go makes 131,078 interpreted original-method callbacks on the current bridge and misses the 60s Linux/Windows bound. Prototype a general cross-process callback mailbox or another measured transport that removes per-callback wakeups without skipping, caching, reordering, or compiling original callback bodies. Preserve interpreter-owned Runner serialization, nested/reentrant callback ownership, output-before-callback ordering, panic/unwind identity, cancellation/process death, receiver reference boundaries, authenticated session identity and native-fallback policy. Work only in an isolated sh branch. First collect an unchanged local control and outside-corpus focused semantics tests; then request a named DO2 slot for one authenticated original-source 60s image row. Do not change fixture/comparator/deadline or merge unmeasured experiments. Windows parity and final full Tour gates follow only after a Linux margin below about 45s is proven.
