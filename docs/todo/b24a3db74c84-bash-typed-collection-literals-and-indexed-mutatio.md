---
id: b24a3db74c84
kind: task
title: Bash++ typed collection literals and indexed mutation
seq: 27
status: assigned
priority: p1
created: 2026-09-06T21:28:34.78452Z
assignee: codex-gpt5.6-sol
sprint: 116
---

First bounded implementation slice of Sprint 116 Story 201 (393c10564e10), on the completed scalar/control contract. Replace the existing raw-word/runtime-reparse collection-literal path with positioned lowering-ready typed AST and interpreted semantics for array, inferred-array, slice, and map literals. Support nested supported collection literals, keyed/unkeyed array/slice elements and map key:value elements; deterministic type/length/key/element/duplicate/out-of-bounds diagnostics; distinct array/slice/map runtime identity; indexed reads; and mutable indexed assignment while preserving deep-readonly alias rejection. Preserve streaming buffered/one-byte identity, Walk/Printer/typed-JSON exact round trips, committed-Go-region ownership, Class-E/top-level/Bash/POSIX fallback, shell expansions, and existing struct/readonly fixtures. Review the preserved 2afc8a73 AST draft for ideas only; do not cherry-pick it and do not make BashPPComposite a Command. Defer structs, pointers/new, slicing, append/copy/delete, equality, and collection range to later children. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Required trailers: Sprint: #116; Story: #201; Story-ID: 393c10564e10.
