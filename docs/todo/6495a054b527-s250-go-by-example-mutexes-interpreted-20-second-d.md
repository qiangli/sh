---
id: 6495a054b527
kind: bug
title: S250 Go by Example mutexes interpreted 20-second deadline
seq: 140
status: done
priority: p0
created: 2026-09-23T11:00:53.085064Z
weave: 1
assignee: qiangli
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-23T11:34:11.090992Z
closed_by: codex-s250
---

Linux Story #89 full Go by Example gate on candidate sh 778ca879/Bashy f72114a: interpreted mutexes.go row 41 killed at original 20s deadline, empty output; oracle and compiled pass. Known from Sprint 219 and Sprint 250 prior validation. The unchanged original performs 30,000 mutex-protected map increments through three WaitGroup.Go workers. Existing sh/interp/gosource_task_capture.md confirms semantic completion but performance is too slow. Profile exact execution and repair a general interpreter cost mechanism without changing original source, deadline, fallback or corpus classification. Verify focused original and controls; manager owns authenticated full Go by Example replay. Parent Sprint 250 Story #89 (235209310249).
