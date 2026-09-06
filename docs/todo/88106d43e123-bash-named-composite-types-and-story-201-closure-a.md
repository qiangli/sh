---
id: 88106d43e123
kind: task
title: Bash++ named composite types and Story 201 closure audit
seq: 31
status: assigned
priority: p1
created: 2026-09-06T22:57:32.590866Z
assignee: codex-gpt5.6-sol
sprint: 116
---

Final bounded closure slice for Sprint 116 Story 201 (393c10564e10), on fea9a161. Audit and close source-reachable named and alias composite/pointer semantics: definitions and aliases whose underlying types are arrays, slices, maps, structs, or pointers; assignment/identity and comparability rules; zero values; new(T); pointer selector/index/slice auto-dereference; and conversions or deterministic rejection where Go requires distinct defined-type identity. Support constant-expression array lengths and validate inferred-array compatibility. Extend slicing/range/equality coverage to dereferenced, selected, indexed, and composite-literal operands wherever the committed typed grammar owns the source; reject unsupported forms deterministically without weakening Class-E fallback. Produce an explicit Story 201 closure matrix and transfer only builtin-owned make/append/copy/delete/len/cap work to Story 204. Preserve readonly paths, task/subshell snapshots, positioned lowering-ready AST, Walk/Printer/typed-JSON exact round trips, buffered/one-byte identity, and Bash/POSIX/classic behavior. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Required trailers: Sprint: #116; Story: #201; Story-ID: 393c10564e10.
