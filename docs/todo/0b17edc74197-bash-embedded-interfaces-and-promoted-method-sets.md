---
id: 0b17edc74197
kind: task
title: Bash++ embedded interfaces and promoted method sets
seq: 34
status: done
priority: p1
created: 2026-09-07T00:27:13.557469Z
assignee: qiangli
sprint: 116
closed: 2026-09-07T00:40:08.710015Z
---

Final Sprint 116 Story 202 slice (73fe43a3368e), based on c7f4edc6. Implement embedded interface elements and method-set composition, including promoted methods, duplicate/conflicting signatures, interface-to-interface assignment/assertion behavior, concrete value and pointer receiver satisfaction, nil and typed-nil dynamics, and deterministic diagnostics. Preserve positioned AST, Walk, Printer, typed-JSON, buffered/one-byte identity, Classic/POSIX/top-level fallback, task/subshell snapshots, value-copy/pointer-identity, and readonly protections. Add adversarial compile/runtime tests. Do not begin Story 203 generics. Exact gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip TestParseConfirm\|TestRunnerRunConfirm -count=1. Required trailers use this child todo seq/id.
