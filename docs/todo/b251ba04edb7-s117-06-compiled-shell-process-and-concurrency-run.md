---
id: b251ba04edb7
kind: task
title: 'S117-06: compiled shell process and concurrency runtime'
seq: 44
status: blocked
priority: p1
created: 2026-09-07T17:54:06.103826Z
sprint: 117
---

Depends S117-03 and agreed types from S117-04. Explicit shell fallback and state, typed-value serialization/decode, structured tasks/channels/select/cancellation, resource ownership and launch-order failure arbitration. No inferred JSON and no leaked tasks/pipes/timers/handles. Parent 59bc1d4ea772.
