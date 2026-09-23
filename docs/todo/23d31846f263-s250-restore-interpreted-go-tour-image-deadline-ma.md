---
id: 23d31846f263
kind: bug
title: S250 restore interpreted Go Tour image deadline margin
seq: 141
status: assigned
priority: p0
created: 2026-09-23T11:36:53.923078Z
weave: 236
assignee: qiangli
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Current published-head Linux Tour candidate Bashy 5353ba3/sh 3d5559eb/harness b4d9349 ran unchanged 291 observations: interpreted _content/tour/solutions/image.go hit original 60s deadline (lineage 60.009s), while baseline and compiled passed. Earlier Story #137 candidate passed at 59.702s with only 0.298s margin. This is a fresh performance-margin recurrence, not a reopening of #137. Preserve original source, 60s limit, stdout/effects/panic/cancellation semantics and native-fallback policy. First focused replay on idle DO leaf under lock; profile and make bounded general interpreter optimization if near deadline. Verify focused controls, then manager authenticated full Tour 291 on current public-head candidate. Parent Sprint 250 Story #89/compatibility gate.

Focused DO replay under the same public-head binary and unchanged 60s timeout
also hit 60.01s (exit 124), stdout/stderr empty. Original source SHA-256
`d78cda6272212f8a73bd991efa502753b4ff36edcd004b21539c2111b16b547f`
matched the harness copy exactly. Artifacts:
`/srv/sprint250/story89-linux-head-5353ba3/image-focused/`.
Coordinate the DO leaf lock before further profiling and run only focused
tests. The sprint manager owns full Tour acceptance replays.

The first bounded callback optimization shipped as sh `4888f8d7` and Bashy
pin `8a68fab1`: it skipped a redundant value-receiver copy and an empty
reference digest. Focused original-source Linux replays passed at 55.21s and
52.05s, with exact baseline stdout and the unchanged 60s deadline. The
manager's clean public-head full Go Tour on harness `c308f5c` then executed
291/291 observations and still hit the interpreted image deadline at 60.011s
(source SHA-256 `d78cda6272212f8a73bd991efa502753b4ff36edcd004b21539c2111b16b547f`).
All 97 baseline, all 97 compiled and the other 96 interpreted observations
passed. Raw lineage and ledger:
`/srv/sprint250/story89-linux-head-8a68fab/go-tour/`.

Next bounded repair: profile the exact Tour executor image row with its fresh
per-row HOME/TMPDIR/tool cache and original fixture, and remove another
general interpreter cost. Target a comfortable margin below about 45s in the
exact executor, preserving value-receiver aliasing, callback effects,
stdout, native-fallback policy and 60s deadline. The manager owns the next
full 291 gate and the DO leaf lock; the agent should run focused controls
locally and ask for one coordinated Linux focused timing after a patch.
