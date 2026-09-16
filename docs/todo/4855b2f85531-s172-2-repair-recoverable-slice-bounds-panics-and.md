---
id: 4855b2f85531
kind: task
title: S172.2 repair recoverable slice-bounds panics and fault-frame preservation
seq: 130
status: assigned
priority: p1
created: 2026-09-16T07:17:36.417895Z
assignee: sprint172-198-manager
sprint: 172
---

Bounded runtime batch: interpreted recover2.go, devirtualization_nil_panics.go, fixedbugs/issue79762.go. Reuse existing panic/runtime-error/frame machinery; preserve classic shell behavior. One outside-corpus reproducer per mechanism; focused tests only. Manager freezes exact targets and runs final merged leaf. No redesign or timeout increases. Respect public/private boundary; code and public tests contain no proprietary planning text.
