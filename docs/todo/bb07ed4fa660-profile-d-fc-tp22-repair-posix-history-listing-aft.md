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

Frozen Profile D Bashy sh cc33b99: fc:22 FAIL; Profile B Bashy FAIL, Profile C GNU Bash PASS. The existing Sprint 88 sh 9d380c9a tab-field change did not close the exact VSC TP. Preserve archived journals and source checksums; reduce the expected-string mismatch with a suite-free POSIX-mode fc -l -n byte test, repair shell behavior if confirmed, and run exact TP22 on a pushed pinned candidate. Do not relabel frozen results.
