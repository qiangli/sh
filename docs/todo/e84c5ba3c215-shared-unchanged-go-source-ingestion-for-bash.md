---
id: e84c5ba3c215
kind: task
title: Shared unchanged Go source ingestion for Bash++
seq: 49
status: done
priority: p0
created: 2026-09-09T03:33:39.703148Z
assignee: qiangli
sprint: 118
closed: 2026-09-09T04:53:59.51765Z
---

Implement W1 from docs/sprint-118-master-execution-plan.md. Original upstream bytes must enter shared positioned typed AST, interpreter and lowerer. No whole-program native Go delegation. Preserve Classic/POSIX and existing language modes. First slice includes Go comments/package/init/main/literals/imports and precise rejection. Parent 6f0c4d9a31be; goal upstream-go.

Review and acceptance,2026-09-08:
Merged6fc00264. Manager shared frontend/typedJSON and syntax gates passed; persistent bridge race gate passed. Broad lower tests passed, canonical120 focused Go-profile and33 Bashsharp lowering cases preserved. Unchanged Go ingestion and default CLI72probes verified. Full Go language/runtime corpus acceptance remains open in6f0c4d9a31be.
