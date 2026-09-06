---
id: efc604eb2b6e
kind: task
title: Bash++ typed expression-switch vertical
seq: 25
status: assigned
priority: p0
created: 2026-09-06T19:03:19.563203Z
assignee: codex-gpt5.6-sol
sprint: 116
---

Fifth bounded slice of Sprint 116 Story 200 (0d36792e0026). Generalize the existing enum-only BashPPSwitch into brace-form Go expression switch semantics inside committed Bash++ function regions: optional scalar init statement, optional scalar tag (including tagless switch), comma-separated scalar case expressions, at most one default, first-match/no-implicit-fallthrough execution, per-clause lexical scope, positioned lowering-ready AST, Walk/Printer/typed-JSON byte-exact round trips, buffered/one-byte identity, deterministic duplicate-default/type diagnostics, and preservation of existing enum switch, top-level Bash++, LangBash, LangPOSIX, and classic shell case/switch-like commands. Do not add break/continue/fallthrough syntax, composites, or Sprint 117 compiled parity; branch-control is a following child. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Required trailers: Sprint #116, Story #200, Story-ID 0d36792e0026.
