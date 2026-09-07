---
id: 145a10476af1
kind: task
title: Bash++ generic functions, constraints, and inference foundation
seq: 35
status: done
priority: p1
created: 2026-09-07T00:50:19.383439Z
assignee: qiangli
sprint: 116
closed: 2026-09-07T01:06:07.495251Z
---

First bounded implementation slice of Sprint 116 Story 203 (a568b882b77e), based on sh 152913b4. Add lowering-ready positioned AST and static/interpreted semantics for generic function type parameters, any/comparable and named interface constraints, explicit instantiation, ordinary call-site type inference, substitution through parameter/result types, and deterministic arity/constraint/inference diagnostics. Preserve Walk/Printer/typed-JSON, buffered/one-byte identity, Classic/POSIX/top-level fallback, existing closures/method/interface semantics, and one runtime value model. Cover scalar and composite/pointer type arguments. Defer generic named types, union/approximation type sets, invalid generic cycles, and final generic method interactions to later Story 203 slices. Exact gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip TestParseConfirm\|TestRunnerRunConfirm -count=1. Required trailers use this child todo seq/id.
