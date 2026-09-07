---
id: b79189e7af8c
kind: task
title: Bash++ interface dispatch and safety closure
seq: 33
status: done
priority: p1
created: 2026-09-07T00:10:41.402874Z
assignee: qiangli
sprint: 116
closed: 2026-09-07T00:26:52.749939Z
---

Second bounded slice of Sprint 116 Story 202 (73fe43a3368e), based on c4a5d467. Close the residual interface contract from child 6e17f7c9c339: accept ordinary Go interface method specifications such as M(int) string with exact parser/printer/Walk/typed-JSON and one-byte behavior; support direct calls through interface values with dynamic receiver dispatch and exact argument/result handling; reject statically impossible assertions deterministically; and enforce readonly mutation rules through interface-held values and aliases. Preserve Classic/POSIX/top-level fallback. Add adversarial tests for value versus pointer receiver method sets, nil interface, typed-nil receiver calls, non-pointer value copies, pointer identity, and fatal diagnostics. Do not implement embedding/promoted members in this slice. Exact gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip TestParseConfirm\|TestRunnerRunConfirm -count=1. Required trailers use this child todo seq/id.
