---
id: 90fb0e829fca
kind: task
title: Bash++ typed branch-control vertical
seq: 26
status: assigned
priority: p0
created: 2026-09-06T19:24:49.636105Z
assignee: codex-gpt5.6-sol
sprint: 116
---

Sixth and final bounded control-flow slice of Sprint 116 Story 200 (0d36792e0026). Implement positioned Bash++ branch nodes and interpreted semantics for unlabeled break, continue, and fallthrough in committed typed control regions. Break targets the innermost typed for/range/switch/select; continue targets the innermost typed for/range even through a nested switch/select; fallthrough is legal only as the final non-empty statement of a non-final expression-switch clause and transfers directly to the next clause without testing it. Provide deterministic illegal-context/placement diagnostics, correct nested scope unwinding, Walk/Printer/typed-JSON byte-exact round trips, buffered/one-byte identity, and regression coverage for loop post behavior after continue and no post after break. Preserve bare shell branch commands outside typed-control context, numbered Classic-shell break/continue, top-level Bash++, LangBash, LangPOSIX, classic shell loops/case, and existing select/range behavior. Labels and goto remain approved compatibility exceptions; do not add them, composites, or Sprint 117 compiled parity. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Required trailers: Sprint #116, Story #200, Story-ID 0d36792e0026.
