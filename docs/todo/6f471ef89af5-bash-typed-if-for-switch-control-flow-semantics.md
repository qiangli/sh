---
id: 6f471ef89af5
kind: task
title: Bash++ typed if/for/switch control-flow semantics
seq: 22
status: doing
priority: p0
created: 2026-09-06T18:02:22.690952Z
assignee: codex-gpt5.6-sol
sprint: 116
---

Second bounded slice of Sprint 116 Story 200 (0d36792e0026). Implement positioned, typed, lowering-ready AST plus interpreted semantics for if, for, and expression switch inside committed Bash++ Go regions, consuming the completed scalar expression contract. Include init/condition/post forms as applicable, nested break/continue, switch fallthrough legality, scope/lifetime behavior, exact diagnostics, Walk/Printer/typed-JSON round trips, buffered and one-byte parsing, and Classic/POSIX/top-level shell preservation. Do not implement composites, interfaces, generics, or Sprint 117 compiled parity. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Commits require Sprint #116, Story #200, Story-ID 0d36792e0026 trailers.
