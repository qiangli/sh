---
id: 0f55f27ae900
kind: task
title: 'S117-05: lower five Bashsharp ergonomics features'
seq: 43
status: done
priority: p1
created: 2026-09-07T17:54:06.082227Z
sprint: 117
closed: 2026-09-08T00:17:56.975716Z
---

Depends S117-04. Defaults, kwargs, enums, deep readonly and null safety lower to ordinary Go with matching checker diagnostics. Existing 33-case lowering gate passes unchanged. Parent 59bc1d4ea772.

Verified at source commit `58b2e283`: the complete short suite, runtime/backend race tests and agentic interpreter race tests pass. The matching product passes all120 profile contracts (105 native artifact executions and15 exact semantic rejections), all33 Bashsharp lowering cases, both interpreted agentic matrices and focused Classic/POSIX isolation. Permanent focused tests also cover actual shared-backend reset, live-pipe/channel authority, scoped constants and exact lexical artifact parity.
