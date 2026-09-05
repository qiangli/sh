---
id: a79e259ab3e6
kind: task
title: Race gate bounds must come from measurement, not the dev box
seq: 17
status: done
priority: p0
created: 2026-09-04T23:02:58.059638Z
assignee: review_run60_ab2_semantics
closed: 2026-09-05T08:30:45Z
sprint: 115
---

The focused lane's 150s outer bound INCLUDED the -race build and sat about 1.3x over the slowest measured lane (114s here), so the ubuntu runner reported TIME on a lane that had not hung. A lane reported as TIME says nothing, which is the exact failure this gate exists to prevent. Warm the race build in its own bounded step so the lane timers measure execution, then set the lane ceilings from measurement with real headroom. A bound here must catch a lifecycle leak that never terminates; that is unbounded, so a generous ceiling catches it just as well and stops manufacturing false timeouts on slower hardware. Global deadline sized to stay inside the job's timeout-minutes so the gate always reports before the job is killed.
