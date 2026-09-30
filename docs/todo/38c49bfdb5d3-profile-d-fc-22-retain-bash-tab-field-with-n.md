---
id: 38c49bfdb5d3
kind: task
title: Profile D fc:22 retain Bash tab field with -n
seq: 3
status: todo
priority: p0
created: 2026-08-31T23:22:08.219936Z
assignee: aurelia-s88
sprint: 100
---

Restore Bash-compatible fc -l -n bytes in POSIX mode: suppress the command number but retain the tab-delimited listing field for explicit/default ranges and multiline entries. Keep focused native byte regressions green; retire fc:22 only after an uncapped Profile D replay on Novi passes at the integrated pin.

## Review 2026-09-30 (steward)

- Status: product fix already integrated - sh 9d380c9a ("retain fc tab field with no numbers", 2026-08-31) is an ancestor of today's head 55289834. Only the uncapped replay is missing.
- Outdated: the named replay host and "integrated pin" refer to Sprint 88; assignee seat aurelia-s88 is gone. Duplicate: umbrella story 81cf6d8ada8b tracks the same fc:22 replay; keep THIS story as the owner.
- Next step: no code work. Take fc:22 (and the other `fc` identities flagged in the Sprint 85 handoff) from the fresh baseline full arm at the frozen candidate (Sprint 110 f093f2d6bba7); if non-PASS, reduce with a focused native byte test and fix.
- Acceptance: fc:22 PASS in the baseline arm at a recorded candidate digest; focused fc byte regressions green.
- Depends on: Sprint 110 baseline arm. Done-candidate once that evidence exists.
