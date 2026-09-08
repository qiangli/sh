---
id: 8394a71ea3cf
kind: task
title: 'S117: implement required new callable AST consumers and reconcile certified source gaps'
seq: 46
status: done
priority: p0
created: 2026-09-07T19:03:37.13412Z
assignee: sprint117-manager
sprint: 117
closed: 2026-09-08T00:17:57.015252Z
---

New positioned AST includes scalar len/cap calls and concrete function signatures; ensure interpreter and compiler consumers support live execution, not definitions-only parsing. Existing advertised source forms var e error, raw string short declaration and parenthesized short expression fail source interpretation. Repair within typed-region ownership, preserving Classic/POSIX behavior, exact regressions, and real Bash confirmation for interpreter changes; do not weaken ledger or fixtures. Separate commits by root cause. Independent generic method parameters remain outside this repair pending scope clarification.

Verified at source commit `58b2e283`: the complete short suite, runtime/backend race tests and agentic interpreter race tests pass. The matching product passes all120 profile contracts (105 native artifact executions and15 exact semantic rejections), all33 Bashsharp lowering cases, both interpreted agentic matrices and focused Classic/POSIX isolation. Permanent focused tests also cover actual shared-backend reset, live-pipe/channel authority, scoped constants and exact lexical artifact parity.
