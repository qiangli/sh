---
id: ca2b5134c913
kind: task
title: Bash++ generic named types, type sets, cycles, and methods
seq: 36
status: doing
priority: p1
created: 2026-09-07T01:14:34.813971Z
assignee: qiangli
sprint: 116
---

Second bounded implementation slice of Sprint 116 Story 203 (a568b882b77e), based on sh 415d3895. Add generic named type declarations and instantiation; union and approximation type-set constraints; deterministic rejection of invalid recursive generic cycles; and applicable generic method/method-set interactions. Preserve positioned AST, Walk/Printer/typed-JSON, buffered/one-byte identity, Classic/POSIX/top-level fallback, interface semantics, and the single runtime value model. Add positive and negative runtime/static diagnostics, including arity, constraint satisfaction, named composite types, pointer/value method sets, and recursive substitution. Exact gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip TestParseConfirm\|TestRunnerRunConfirm -count=1.
