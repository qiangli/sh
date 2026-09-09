---
id: e84c5ba3c215
kind: task
title: Shared unchanged Go source ingestion for Bash++
seq: 49
status: todo
priority: p0
created: 2026-09-09T03:33:39.703148Z
sprint: 118
---

Implement W1 from docs/sprint-118-master-execution-plan.md. Original upstream bytes must enter shared positioned typed AST, interpreter and lowerer. No whole-program native Go delegation. Preserve Classic/POSIX and existing language modes. First slice includes Go comments/package/init/main/literals/imports and precise rejection. Parent 6f0c4d9a31be; goal upstream-go.
