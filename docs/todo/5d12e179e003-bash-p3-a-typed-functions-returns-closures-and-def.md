---
id: 5d12e179e003
kind: task
title: Bash++ P3-A typed functions returns closures and defer
seq: 14
status: done
priority: p0
created: 2026-09-03T16:35:50.220309Z
weave: 31
assignee: qiangli
sprint: 113
closed: 2026-09-03T18:39:33.816086Z
---

Implement typed Bash++ function declarations with typed/untyped params, named/multiple results, explicit/bare return, lexical closures/recursion, and typed LIFO defer with captured arguments and correct failure/control-flow semantics. Add parser AST Walk Printer typedjson and interpreter coverage. Must not intercept ordinary shell commands named defer; preserve Classic/POSIX/Class-E fallback and one-byte streaming. Exclude channels/process lowering.
