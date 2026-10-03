---
id: f59cde37a1e3
kind: bug
title: Keep private PPID bridge out of one-file utility applets
seq: 163
status: assigned
priority: p0
created: 2026-10-03T13:28:27.046526Z
assignee: codex-gpt6.1-sol
sprint: 355
sprint_id: 3a83ff48-7f8b-5be4-b0e6-e146762b2573
sprint_title: Profile D residual blocker triage and targeted closure
---

Full Profile D at:89 and batch:8 compare a scheduled job environment against an env applet snapshot. The one-file env link shares the Bashy inode, so sh/interp childParentPIDBridge injects BASHY_PARENT_PID into env even though only shell startup consumes it. Restrict the bridge to shell entry routes while retaining exact executable identity and PPID behavior; verify utility alias exclusion and shell bridge preservation with focused tests. Evidence: ~/.vsc-do/s355-full-osusergo-20261003/evidence/live-new-at-batch/{at-0075be,batch-0076be}.journal.
