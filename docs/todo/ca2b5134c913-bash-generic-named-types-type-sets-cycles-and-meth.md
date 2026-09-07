---
id: ca2b5134c913
kind: task
title: Bash++ generic named types, type sets, cycles, and methods
seq: 36
status: doing
priority: p0
created: 2026-09-07T01:14:34.813971Z
assignee: qiangli
sprint: 116
---

Corrective closure slice of Sprint 116 Story 203 (a568b882b77e), based on sh 53913332. Prior foundation is integrated but manager rejected closure. Required manager failures are preserved at refs/salvage/abandoned-26-adecc106efb4db024452fedf087b66ce79144072: TestBashPPGenericRecursiveIndirectionAccepted proves legal type List[T any] []List[T] currently emits undefined element type; TestBashPPGenericReceiverDeclaration proves ordinary func (b Box[T]) Show() is rejected. Restore those exact tests first. Implement representation-aware generic cycle legality (direct/array/struct value cycles invalid; pointer/slice/map indirection allowed), cycle-safe validation and zero-value paths, receiver type-parameter parsing/binding/substitution, instantiated pointer/value method sets, deterministic rejection of independent method type parameters, and rejection of union/approximation expressions as concrete type arguments. Cover buffered/one-byte parse-print-typedJSON and Classic/POSIX fallback. Do not close until exact manager tests and gate pass. Exact gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip TestParseConfirm\|TestRunnerRunConfirm -count=1.
