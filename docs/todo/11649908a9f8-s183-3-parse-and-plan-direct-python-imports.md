---
id: 11649908a9f8
kind: task
title: S183.3 — parse and plan direct Python imports
seq: 115
status: done
priority: p0
created: 2026-09-15T00:07:55.305604Z
assignee: codex-gpt5.6-sol
sprint: 183
closed: 2026-09-15T01:55:47.527799Z
closed_by: codex-gpt5.6-sol
---

Extend syntax.BashPPImport with an explicit foreign-import variant for `import python[environment] "module.path" as alias` (omitted alias derives safely). Preserve existing Go import forms and Classic/POSIX fallback. Cover parser/printer/walk/typed-JSON, positions, alias/collision rules, lazy immutable import planning, and no import during parse/check. Consume S183.2 EnvironmentPlan; do not create another discovery path. Sprint: #183.
