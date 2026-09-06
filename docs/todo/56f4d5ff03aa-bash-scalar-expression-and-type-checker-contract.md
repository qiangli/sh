---
id: 56f4d5ff03aa
kind: task
title: Bash++ scalar expression and type-checker contract
seq: 21
status: doing
priority: p0
created: 2026-09-06T16:18:00Z
assignee: claude-opus5
sprint: 116
---

First bounded slice of parent story `0d36792e0026`. Define and implement the
single recursive typed expression/value contract needed by later Sprint 116
stories, limited here to identifiers, scalar literals, parentheses, unary and
binary operators, typed and untyped constants, scalar conversions, comparison,
and compile-time diagnostics. Preserve exact positions, Walk/Printer/typed-JSON
round trips, one-byte streaming, committed start-site fallback, and Classic/
POSIX isolation. Do not implement composite values, interfaces, generics, or
control flow in this slice, and do not edit the umbrella coverage ledger;
instead record the exact tests/evidence the ledger lane must consume.

Gate: focused syntax/typedjson/interpreter tests for the new scalar matrix and
invalid forms, plus `go test ./syntax ./syntax/typedjson ./interp -skip
'TestParseConfirm|TestRunnerRunConfirm' -count=1` with the safe host PATH. The
commit must carry Sprint 116, Story 200, and Story-ID `0d36792e0026` trailers.
