---
id: 56f4d5ff03aa
kind: task
title: Bash++ scalar expression and type-checker contract
seq: 21
status: done
priority: p0
created: 2026-09-06T16:18:00Z
assignee: codex-gpt5.6-sol
sprint: 116
closed: 2026-09-06T17:01:48.148154Z
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

Manager review (2026-09-06): candidate `24de2e28` passed its configured gate
but was rejected as the shared contract. It made operators and conversions
AST-only when shell tokenization prevented them reaching the evaluator, kept
the legacy `n := y` behavior of binding the literal string `y`, bounded
constants to `int64`/`float64`, and left an untracked artifact. A replacement
must prove every claimed form end to end from Bash++ source, resolve bare
identifiers consistently inside committed Go regions, use Go-compatible
constant semantics (for example `go/constant`), and keep unreachable syntax
out of the delivered claim until a start site can carry it.
