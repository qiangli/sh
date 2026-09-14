---
id: 602fb4253c61
kind: task
title: S182.3 — analyze and emit TypeScript through the official compiler API
seq: 111
status: done
priority: p1
created: 2026-09-14T22:11:44.512821Z
assignee: codex-gpt5.6-sol
sprint: 182
closed: 2026-09-14T22:30:09.532996Z
closed_by: codex-gpt5.6-sol
---

Use the official typescript npm compiler module for AST parsing, syntactic/semantic diagnostics, export discovery, type mapping, and CommonJS emission. Permit declaration-safe TypeScript forms and reject imports or uncontrolled top-level execution in this slice. Gate: analyzer tests with a pinned official compiler. Depends on S182.2. Sprint: #182.
