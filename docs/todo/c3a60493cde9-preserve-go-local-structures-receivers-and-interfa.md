---
id: c3a60493cde9
kind: task
title: Preserve Go local structures receivers and interface values
seq: 54
status: done
priority: p1
created: 2026-09-09T06:39:40.110712Z
weave: 76
assignee: qiangli
sprint: 118
closed: 2026-09-09T21:10:00Z
resolution: fixed
---

Resolved by sh commits 0cab6bad and 63c7c0da. Candidate021 executes the unchanged custom-errors example successfully in oracle, compiled, and interpreted modes, while the complete Tour remains 291/291. The runtime preserves recover/interface values and the reviewed errors.AsType path without broad dependency-mutation permission.
