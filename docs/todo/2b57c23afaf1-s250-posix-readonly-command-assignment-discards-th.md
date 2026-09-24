---
id: 2b57c23afaf1
kind: bug
title: S250 POSIX readonly command assignment discards the physical line
seq: 153
status: done
priority: p0
created: 2026-09-23T22:39:01.961561Z
assignee: codex-s250
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-24T02:13:18.202033Z
closed_by: codex-s250
---

Licensed DO2 POSIX.shell shell-only arm s250-v0280-shell-20260923-r1 at approved Bashy 995f401 / sh d18f24b completed 1 set and 493 execution TPs with zero caps or runner failures, but pass-group 492 and one blocker: sh_07 TP7. Immutable retrieval PASS; public ledger SHA256 a4a18e3949b035a2eddf12e5c2a092b1e8d8c9205c911ed5b713975d756ec0b6. No licensed suite source is copied here. Independent minimal GNU Bash 5.3 probe in noninteractive POSIX mode: readonly ro=x; ro=y printf "continued\\n"; printf "after\\n". GNU discards the rest of the physical line and reaches EOF with status 1; the original Bashy continued within that line, emitted after, and exited 0. Repair command-prefixed readonly assignment handling in noninteractive POSIX mode; preserve non-POSIX and interactive behavior. Acceptance: focused GNU/Bashy differential with output/status controls; original sh regression gates; fresh pushed candidate and supervisor approval; exact unchanged licensed 493 shell-only rerun and immutable retrieval with 493/493 pass-group, zero blockers/caps/runner failures. Do not change licensed source, fixtures, limits, or run utility/full profiles.

Correction after GNU Bash 5.3's public `errors7.sub` regression: the non-special command-prefix error discards the rest of its **physical line**, then resumes at the next line. The same-line probe above ends with status 1 because the shell reaches EOF after the discarded tail; it does not prove a shell-wide fatal exit. In a named-file control, `x=8 notthere` and `x=8 echo ...` each followed by a separate `echo after ...: $?` line both resume with status 1 for the failed command. Preserve that distinction; special builtins retain their separate POSIX fatal path.

Accepted 2026-09-24: the final strict pure `sh` SUT using sh `72bb8cdcec0f2e07405d8c7aea96e93aa7698665` and Bashy `7416f0b8941b957f1feb1c9496a648f4f30a563d` passed the public readonly named-file probe and approved shell-only VSC Profile B arm `s250-v0280-shell-20260924-r4`. The unchanged 493-purpose set scored 493/493 certification PASS-group with zero blockers, caps, runner failures, or missing purposes; the private DAG gate exited zero. Immutable public ledger SHA-256 `6ac2daf9ec384566e295e78182780885f9f964ecd87794183fa6e2fb0fe37aad`; TP summary SHA-256 `b447e40df7281000d953aade103097d4cc8e7dfecdad23df2ac9291296ea78af`. Licensed source remains outside this repository. The GNU Bash 5.3 86-fixture gate on the same product bytes passed 86/86, preserving the separate GNU-compatible `bash` entrypoint.
