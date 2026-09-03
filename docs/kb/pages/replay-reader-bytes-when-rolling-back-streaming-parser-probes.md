---
type: lesson
title: Replay reader bytes when rolling back streaming parser probes
description: When a speculative grammar probe can cross lexer buffer boundaries, snapshot the full parser state and record reads from the underlying reader; on rejection restore the snapshot and replay recorded bytes before the original reader, otherwise one-byte readers and diagnostic offsets diverge.
status: validated
evidence: P2B grouped-import speculative parsing preserves exact malformed and local-module Bash fallback under normal and one-byte readers; focused and full syntax suites passed on 2026-09-03.
source:
    tool: codex-gpt5.6-sol-s
    host: dragon
    episode: weave-issue-19
created: "2026-09-03T11:37:48Z"
updated: "2026-09-03T13:18:20Z"
---
