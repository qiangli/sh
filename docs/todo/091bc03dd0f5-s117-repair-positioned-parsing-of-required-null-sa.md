---
id: 091bc03dd0f5
kind: task
title: 'S117: repair positioned parsing of required null-safety fixture forms'
seq: 45
status: done
priority: p0
created: 2026-09-07T18:44:48.252922Z
assignee: sprint117-manager
sprint: 117
closed: 2026-09-07T19:01:58.901749Z
---

Existing unchanged Bashsharp lowering fixtures false-positive-guards and unsafe-call do not reach positioned AST: nested call in logical typed condition and concrete func-typed parameter. Repair only these required forms preserving dialect classification and positions; add bytewise/parse-print/typedjson and Classic-POSIX isolation tests. Parent story0f55f27ae900, required33-case gate. No fixture rewriting or whole-source Go reparse.
