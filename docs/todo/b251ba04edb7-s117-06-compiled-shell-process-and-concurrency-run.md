---
id: b251ba04edb7
kind: task
title: 'S117-06: compiled shell process and concurrency runtime'
seq: 44
status: done
priority: p1
created: 2026-09-07T17:54:06.103826Z
assignee: claude-opus5
sprint: 117
closed: 2026-09-08T00:17:56.995397Z
---

Depends S117-03 and agreed types from S117-04. Explicit shell fallback and state, typed-value serialization/decode, structured tasks/channels/select/cancellation, resource ownership and launch-order failure arbitration. No inferred JSON and no leaked tasks/pipes/timers/handles. Parent 59bc1d4ea772.

Verified at source commit `58b2e283`: the complete short suite, runtime/backend race tests and agentic interpreter race tests pass. The matching product passes all120 profile contracts (105 native artifact executions and15 exact semantic rejections), all33 Bashsharp lowering cases, both interpreted agentic matrices and focused Classic/POSIX isolation. Permanent focused tests also cover actual shared-backend reset, live-pipe/channel authority, scoped constants and exact lexical artifact parity.
