---
id: 204841146c97
kind: task
title: Bash++ typed structs, selectors, and value semantics
seq: 28
status: done
priority: p1
created: 2026-09-06T21:48:15.662276Z
assignee: codex-gpt5.6-sol
sprint: 116
closed: 2026-09-06T22:10:08.169033Z
---

Second bounded implementation slice of Sprint 116 Story 201 (393c10564e10), on f1b95b1e. Replace the remaining raw-word/runtime-go-parser struct path with positioned lowering-ready typed AST and interpreted semantics for named struct declarations/literals and anonymous struct literals where source-reachable. Support keyed and positional literals without mixing, nested supported structs/collections, declared field types and zero values, selector reads, mutable selector assignment, duplicate/unknown/missing-or-extra positional field diagnostics, and Go value-copy semantics for structs and nested arrays while retaining slice/map reference semantics. Preserve deep-readonly rejection through aliases/paths, method-value and selector/call disambiguation, task/subshell snapshot isolation, Walk/Printer/typed-JSON exact round trips, buffered/one-byte identity, and Class-E/top-level/Bash/POSIX fallback. Do not add embedding/method-set promotion (Story 202), pointers/new, slicing, equality, collection range, or compiled parity. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Required trailers: Sprint: #116; Story: #201; Story-ID: 393c10564e10.
