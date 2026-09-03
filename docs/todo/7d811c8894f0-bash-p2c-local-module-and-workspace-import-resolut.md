---
id: 7d811c8894f0
kind: task
title: 'Bash++ P2C: local module and workspace import resolution'
seq: 13
status: done
priority: p0
created: 2026-09-03T14:38:19.41903Z
weave: 29
assignee: qiangli
sprint: 98
---

Implement Bash++ P2C import resolution on current sh master after P2A/P2B. Support exact Go local module, workspace replace/use, vendor, and GOPATH resolution semantics for ordinary, aliased, blank, and dot imports; reject internal/vendor visibility violations and path traversal; bind packages into a session-scoped namespace with deterministic collision/duplicate rules and no global leakage. Preserve stdlib allowlist and Classic/POSIX fallback. Add real temporary-module tests, concurrent sessions/race tests, parse-walk-print-interp coverage, and exact negative cases. Reuse the existing replaceable evaluator boundary; no second value model.

Delivered by `84f8982687667dd1457a78485b8306940767ab96`. Independent review and
focused normal/race tests cover local modules, workspaces, replace/use,
vendor and GOPATH lookup, visibility boundaries, traversal rejection, and
session isolation.
