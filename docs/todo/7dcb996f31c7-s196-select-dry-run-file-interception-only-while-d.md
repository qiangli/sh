---
id: 7dcb996f31c7
kind: task
title: S196 select dry-run file interception only while dry-run is active
seq: 133
status: assigned
priority: p0
created: 2026-09-16T11:18:21.646288Z
assignee: codex-gpt5.6-sol
sprint: 196
---

Add narrowly scoped dry-run-only open interception for the existing dry-run option, preserving native cooperative task opens during normal execution. Preserve Reset/Subshell snapshots and dynamic set -o dryrun. Active arbitrary custom handlers, including the dry-run override while active, remain refused in tasks; do not bypass FIFO/cancellation safety or allow dry-run filesystem mutation. Reproduce product integration externally; add engine regressions for ordinary task writes, initial/runtime toggles, reset, and refusal.
