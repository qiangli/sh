---
id: 39a5cc92cb21
kind: task
title: Bash++ typed for-loop vertical
seq: 24
status: assigned
priority: p0
created: 2026-09-06T18:46:07.802904Z
assignee: codex-gpt5.6-sol
sprint: 116
---

Fourth bounded slice of Sprint 116 Story 200 (0d36792e0026). Implement typed brace-form Go for statements inside committed Bash++ function regions: infinite, condition-only, and three-clause forms; positioned/lowering-ready AST; applicable scalar initializer and post statements; correct scope and Go 1.27 per-iteration loop-variable behavior; Walk/Printer/typed-JSON exact round trips; buffered/one-byte identity; deterministic diagnostics; and preservation of top-level Bash++, Bash, POSIX, classic shell for, arithmetic for, and existing Bash++ range. Do not add switch, break/continue/fallthrough, composites, or Sprint 117 compiled parity. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Required trailers: Sprint #116, Story #200, Story-ID 0d36792e0026.
