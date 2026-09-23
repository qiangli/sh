---
id: 2b57c23afaf1
kind: bug
title: S250 POSIX readonly assignment before command must abort noninteractive shell
seq: 153
status: todo
priority: p0
created: 2026-09-23T22:39:01.961561Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Licensed DO2 POSIX.shell shell-only arm s250-v0280-shell-20260923-r1 at approved Bashy 995f401 / sh d18f24b completed 1 set and 493 execution TPs with zero caps or runner failures, but pass-group 492 and one blocker: sh_07 TP7. Immutable retrieval PASS; public ledger SHA256 a4a18e3949b035a2eddf12e5c2a092b1e8d8c9205c911ed5b713975d756ec0b6. No licensed suite source is copied here. Independent minimal GNU Bash 5.3 probe in noninteractive POSIX mode: readonly ro=x; ro=y printf "continued\\n"; printf "after\\n". GNU exits status 1 after readonly error, whereas current Bashy reports readonly error, emits after, and exits 0. Repair only assignment-error fatality for command-prefixed readonly assignment in noninteractive POSIX mode; preserve non-POSIX and interactive behavior. Acceptance: focused GNU/Bashy differential with output/status controls; original sh regression gates; fresh pushed candidate and supervisor approval; exact unchanged licensed 493 shell-only rerun and immutable retrieval with 493/493 pass-group, zero blockers/caps/runner failures. Do not change licensed source, fixtures, limits, or run utility/full profiles.
