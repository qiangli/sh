---
id: 4cf6f6fed919
kind: bug
title: Deliver trapped TERM when a foreground child dies in the same process-group signal
seq: 159
status: done
priority: p0
labels:
    - linux
    - posix
created: 2026-10-02T07:51:50.951129Z
assignee: codex-gpt6.1-sol
sprint: 355
sprint_id: 3a83ff48-7f8b-5be4-b0e6-e146762b2573
sprint_title: Profile D residual blocker triage and targeted closure
closed: 2026-10-02T09:30:02.695007Z
closed_by: codex-gpt6.1-sol
---

Cross-links umbrella Sprint355 process story a481a106ffd0. Exact D kill:19 still FAIL on pinned candidate 2ea9e36/sh 1c3d35912: one child TERM trap marker absent after external kill 0. Valid suite-free Linux process groups show 3/8 Bashy+Go misses after 30s and 2/8 Bashy+system misses, while GNU+Go 8/8 and GNU+system 4/4 pass; all pre-signal groups contain four shells and three Go sleeps. Missing Bashy trap trace remains absent 30s later and all processes exited. Diagnose signal receipt versus foreground child wait/Runner finalization, implement minimal fix, and add a meaningful Linux process regression red before/green after; run sh and Bashy release CI plus targeted D replay. Preserve raw TP19 FAIL until replay.

Public regression checkpoint: the identical eight-group Linux test fails 3/3 on sh 1c3d35912 after a two-second marker grace. The isolated fix passes 10/10 repetitions. It waits at most 200 ms for an active matching shell trap only when a foreground external child in the shell's process group is reaped as signaled; ordinary completions and separate job groups have no wait. The signal forwarder now publishes the pending bit before waking receipt waiters. A command-only TERM control checks that the shell trap does not run and the bounded wait ends. Candidate acceptance still requires sh CI, Bashy release gates, and targeted D replay.
