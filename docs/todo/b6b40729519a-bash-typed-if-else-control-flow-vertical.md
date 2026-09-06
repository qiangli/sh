---
id: b6b40729519a
kind: task
title: Bash++ typed if/else control-flow vertical
seq: 23
status: done
priority: p0
created: 2026-09-06T18:18:30.464891Z
assignee: codex-gpt5.6-sol
sprint: 116
closed: 2026-09-06T18:33:14.17898Z
---

Third bounded slice of Sprint 116 Story 200 (0d36792e0026), and first control-flow vertical. Implement only brace-form Go if/else-if/else inside an already committed Bash++ function region. Deliver positioned typed AST, parser transaction/fallback safety, Walk/Printer/typed-JSON exact round trips, evaluator boolean/type diagnostics and Go scope/lifetime for optional init statements, buffered vs one-byte identity, and explicit preservation tests for top-level Bash++, LangBash, LangPOSIX, and classic shell if inside functions. Do not add for, switch, break, continue, fallthrough, or speculative nodes. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Commits require Sprint #116, Story #200, Story-ID 0d36792e0026.
