---
id: "377638503038"
kind: task
title: Bash++ composite slicing, nil, equality, and range semantics
seq: 30
status: assigned
priority: p1
created: 2026-09-06T22:38:36.281344Z
assignee: codex-gpt5.6-sol
sprint: 116
---

Fourth bounded implementation slice of Sprint 116 Story 201 (393c10564e10), on 0ebfbc63. Complete source-reachable Go 1.27 composite operations: full and indexed slicing forms with bounds/capacity validation and correct array-versus-slice identity; comparable equality for arrays and structs; pointer identity and nil comparison; slice/map nil-only comparison with deterministic rejection of other comparisons; true nil zero values distinct from allocated empty slice/map values; and typed range over arrays, slices, and maps with Go key/value binding and mutation/snapshot behavior. Preserve deep-readonly enforcement, addressability, value-copy versus reference identity, task/subshell isolation, positioned lowering-ready AST, Walk/Printer/typed-JSON exact round trips, buffered/one-byte identity, and Class-E/top-level/Bash/POSIX fallback. Audit and cover nil dereference and pointer equality without regressing selectors or method behavior. Defer append/copy/delete and other builtins to Story 204; defer embedding/method sets, generics, and compiled parity. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip TestParseConfirm\|TestRunnerRunConfirm -count=1. Required trailers: Sprint: #116; Story: #201; Story-ID: 393c10564e10.
