---
id: 8e542f67fee8
kind: task
title: S173.2 implement Go blank struct field semantics
seq: 131
status: assigned
priority: p1
created: 2026-09-16T07:46:00.111672Z
assignee: sprint172-198-manager
sprint: 173
---

Bounded shared mechanism for issue21048, issue31546, abi/convT64_criteria. Permit repeated blank fields; evaluate and discard blank initializers; omit blank values at native bridge while preserving declaration layout. Independent differential reproducer and negative ordinary-field checks. No value-model redesign. Manager final gate and authenticated leaf under S173.3.
