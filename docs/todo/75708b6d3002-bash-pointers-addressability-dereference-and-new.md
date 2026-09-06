---
id: 75708b6d3002
kind: task
title: Bash++ pointers, addressability, dereference, and new
seq: 29
status: assigned
priority: p1
created: 2026-09-06T22:15:23.219146Z
assignee: codex-gpt5.6-sol
sprint: 116
---

Third bounded implementation slice of Sprint 116 Story 201 (393c10564e10), on ab056a64. Add positioned lowering-ready typed AST and interpreted semantics for pointer types, address-of and dereference expressions, dereference assignment, nil pointer zero values, and new(T) allocation across supported named scalars, structs, arrays, slices, and maps. Enforce Go addressability for identifiers, selectors, and index targets (including rejecting map-element addresses), preserve existing receiver/method expression behavior, deep-readonly paths and reference identity, and task/subshell snapshot isolation. Add deterministic diagnostics for nil dereference, non-addressable operands, invalid pointer targets/types, and assignment mismatch. Cover Walk/Printer/typed-JSON exact round trips, buffered/one-byte identity, and Class-E/top-level/Bash/POSIX fallback. Defer slicing, general equality, collection range, embedding/method sets, generics, and compiled parity. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Required trailers: Sprint: #116; Story: #201; Story-ID: 393c10564e10.
