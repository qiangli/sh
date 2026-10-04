---
id: f59cde37a1e3
kind: bug
title: Keep private PPID bridge out of one-file utility applets
seq: 163
status: done
priority: p0
created: 2026-10-03T13:28:27.046526Z
assignee: codex-gpt6-sol
sprint: 355
sprint_id: 3a83ff48-7f8b-5be4-b0e6-e146762b2573
sprint_title: Profile D residual blocker triage and targeted closure
closed: 2026-10-04T07:13:45.4611Z
closed_by: codex-gpt6-sol
---

Full Profile D at:89 and batch:8 compare a scheduled job environment against an env applet snapshot. The one-file env link shares the Bashy inode, so sh/interp childParentPIDBridge injects BASHY_PARENT_PID into env even though only shell startup consumes it. Restrict the bridge to shell entry routes while retaining exact executable identity and PPID behavior; verify utility alias exclusion and shell bridge preservation with focused tests. Evidence: ~/.vsc-do/s355-full-osusergo-20261003/evidence/live-new-at-batch/{at-0075be,batch-0076be}.journal.

## Sprint 355 acceptance evidence 2026-10-04

PPID bridge route isolation is merged at 90432168d and pinned in full6. The complete candidate has no new at/batch/env failure or regression. Historical raw journals and any pending formal certification decisions are unchanged.
