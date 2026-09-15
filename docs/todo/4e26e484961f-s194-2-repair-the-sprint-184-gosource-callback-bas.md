---
id: 4e26e484961f
kind: task
title: S194.2 repair the Sprint 184 GoSource callback baseline
seq: 123
status: todo
priority: p0
created: 2026-09-15T12:05:21.439633Z
sprint: 194
---

Before editing, remeasure the exact S184 before/after commits in isolated worktrees and record failing test names, elapsed time and first errors; reconcile the later 3/8-failure drift. Add outside-corpus regressions and fix the general callback lifecycle/policy causes. No timeout increase, expected-failure list, fixture identity special-case or hidden red. Gate: focused old/new comparison and go test -short ./... completes green; gofmt and git diff --check.
