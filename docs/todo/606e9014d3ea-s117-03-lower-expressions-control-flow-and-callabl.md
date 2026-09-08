---
id: 606e9014d3ea
kind: task
title: 'S117-03: lower expressions control flow and callables'
seq: 41
status: done
priority: p1
created: 2026-09-07T17:54:06.040446Z
assignee: sprint117-manager
sprint: 117
closed: 2026-09-08T00:17:56.933845Z
---

Depends S117-02 and S117-01. Lower declarations/constants/conversions/operators/control flow/imports/functions/methods/results/closures/handles/return/defer/panic-recover using certified semantics. Deterministic source diagnostics, binding and evaluation order. Parent 59bc1d4ea772.

Verified at source commit `58b2e283`: the complete short suite, runtime/backend race tests and agentic interpreter race tests pass. The matching product passes all120 profile contracts (105 native artifact executions and15 exact semantic rejections), all33 Bashsharp lowering cases, both interpreted agentic matrices and focused Classic/POSIX isolation. Permanent focused tests also cover actual shared-backend reset, live-pipe/channel authority, scoped constants and exact lexical artifact parity.
