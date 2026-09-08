---
id: 28a09f48b5be
kind: task
title: 'S117-04: lower composite types interfaces generics and builtins'
seq: 42
status: done
priority: p1
created: 2026-09-07T17:54:06.060829Z
assignee: sprint117-manager
sprint: 117
closed: 2026-09-08T00:17:56.955803Z
---

Depends S117-03. Complete arrays/slices/maps/structs/pointers/aliases and copies, interfaces/assertions/embedding/method sets, generics/constraints/inference, applicable range and builtins. All applicable current Go-profile rows have compiled evidence, approved exclusions unchanged. Parent 59bc1d4ea772.

Verified at source commit `58b2e283`: the complete short suite, runtime/backend race tests and agentic interpreter race tests pass. The matching product passes all120 profile contracts (105 native artifact executions and15 exact semantic rejections), all33 Bashsharp lowering cases, both interpreted agentic matrices and focused Classic/POSIX isolation. Permanent focused tests also cover actual shared-backend reset, live-pipe/channel authority, scoped constants and exact lexical artifact parity.
