---
id: bb07ed4fa660
kind: bug
title: 'Profile D fc TP22: repair POSIX history listing after frozen failure'
seq: 156
status: todo
priority: p0
created: 2026-10-02T01:04:45.923126Z
sprint: 341
sprint_id: 5f262cbb-e61a-5a5e-8361-c60190adf78f
sprint_title: 'POSIX certification: base XCU claim, pure Go, Linux x86_64 - fresh baseline, failure list, final run'
---

Frozen Profile D Bashy sh cc33b99: fc:22 FAIL; Profile B Bashy FAIL, Profile C GNU Bash PASS. The existing Sprint 88 sh 9d380c9a tab-field change did not close the exact VSC TP. The journal contains the current session's five commands but no prior `fc -l`. The licensed TP starts a fresh interactive `sh` and tests that an earlier `fc -l` appears in the listing; its suite script exports HISTFILE. Bashy's CLI deliberately uses `PlainTerminal` when invoked as `sh`; that fallback loop recorded input only in memory, whereas the readline and assumed-TTY loops persist entries. A new focused interactive test failed because the history file was absent and passes after the fallback loop appends each recorded input through the existing file-history writer. Preserve archived journals and source checksums, then require exact TP22 on a pushed pinned candidate before closure. Do not relabel frozen results.
