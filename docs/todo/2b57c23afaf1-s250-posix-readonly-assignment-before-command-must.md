---
id: 2b57c23afaf1
kind: bug
title: S250 POSIX readonly command assignment discards the physical line
seq: 153
status: todo
priority: p0
created: 2026-09-23T22:39:01.961561Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Licensed DO2 POSIX.shell shell-only arm s250-v0280-shell-20260923-r1 at approved Bashy 995f401 / sh d18f24b completed 1 set and 493 execution TPs with zero caps or runner failures, but pass-group 492 and one blocker: sh_07 TP7. Immutable retrieval PASS; public ledger SHA256 a4a18e3949b035a2eddf12e5c2a092b1e8d8c9205c911ed5b713975d756ec0b6. No licensed suite source is copied here. Independent minimal GNU Bash 5.3 probe in noninteractive POSIX mode: readonly ro=x; ro=y printf "continued\\n"; printf "after\\n". GNU discards the rest of the physical line and reaches EOF with status 1; the original Bashy continued within that line, emitted after, and exited 0. Repair command-prefixed readonly assignment handling in noninteractive POSIX mode; preserve non-POSIX and interactive behavior. Acceptance: focused GNU/Bashy differential with output/status controls; original sh regression gates; fresh pushed candidate and supervisor approval; exact unchanged licensed 493 shell-only rerun and immutable retrieval with 493/493 pass-group, zero blockers/caps/runner failures. Do not change licensed source, fixtures, limits, or run utility/full profiles.

Correction after GNU Bash 5.3's public `errors7.sub` regression: the non-special command-prefix error discards the rest of its **physical line**, then resumes at the next line. The same-line probe above ends with status 1 because the shell reaches EOF after the discarded tail; it does not prove a shell-wide fatal exit. In a named-file control, `x=8 notthere` and `x=8 echo ...` each followed by a separate `echo after ...: $?` line both resume with status 1 for the failed command. Preserve that distinction; special builtins retain their separate POSIX fatal path.
