---
type: lesson
title: Replay reader bytes when rolling back streaming parser probes
description: When a speculative grammar probe can cross lexer buffer boundaries, snapshot the full parser state and record reads from the underlying reader; on rejection restore the snapshot and replay recorded bytes before the original reader, otherwise one-byte readers and diagnostic offsets diverge.
status: validated
evidence: Issue 24 exhaustive Go 1.27 import allowlist near-miss matrix preserves exact LangBash AST and diagnostic type/text under one-byte reads; focused race and full suites passed on 2026-09-03.
source:
    tool: codex-gpt5.6-sol-s
    host: dragon
    episode: weave-issue-19
created: "2026-09-03T11:37:48Z"
updated: "2026-09-03T12:56:42Z"
---
