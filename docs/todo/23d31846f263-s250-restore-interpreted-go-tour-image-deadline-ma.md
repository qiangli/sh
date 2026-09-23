---
id: 23d31846f263
kind: bug
title: S250 restore interpreted Go Tour image deadline margin
seq: 141
status: assigned
priority: p0
created: 2026-09-23T11:36:53.923078Z
weave: 237
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

The second bounded agent submitted local candidate `cf2b5320`: focused image
allocations fell 18.2%, but the authenticated exact Linux executor row passed
at 58.399s, leaving only 1.601s under the original deadline. Its earlier
three-file subset timed out at 60.008s. The candidate remains unintegrated.
A one-second process sample of the exact 60.006s timeout shows native helper
compilation in the first ~2s; at elapsed 59s Bashy had used 48s CPU and the
generated `bashpp-session` helper 18s CPU, both still in the callback phase.
Evidence: `/srv/sprint250/story141-linux-opt236/go-tour-image-sampled/` and
`/srv/sprint250/story141-linux-cf2/go-tour-image-exact/`.

Next bounded agent: find one measured, general repeated callback cost on the
exact executor path and make a minimal sound repair that gets image below
about 45s. Do not repeat allocation tweaks without Linux timing. A buffered
JSON socket trial had no local timing benefit and was discarded. Keep the
original fixture, 60s limit, effect ordering, cancellation and fallback
policy. Coordinate one focused DO run under the leaf lock; the manager owns
the full 291 acceptance gate.

An unchanged public-head candidate was independently replayed on the other
authorized DigitalOcean test droplet (`vsc-s129-shell-20260908`,
`138.68.155.86`, two vCPUs) under its `/srv/sprint219/leaf.lock`. Candidate
manifest SHA-256 `e34f92359318640e55df4d81b1f08d3abe2d799fb0cb49e65dd3dccc5bdab2cb`,
Bashy binary SHA-256 `f7d5c4054e7e18a46924af2f1cc012ac2d6b3ab0615598ef1f5bccc8a3673ad6`,
Bashy `8a68fab1`, sh `4888f8d7`, and harness `c308f5c` matched the first
droplet. The original executor's one-row diagnostic authenticated the
candidate; baseline and compiled image passed, but interpreted image again
timed out at the unchanged 60s limit (60.012s). The partial ledger SHA-256 is
`09716ba3f3ed4a1c196ba34a50f556dfda671fcb1465cec35ea2329670dbe52f`
at `/srv/sprint250/story141-do2-head-8a68fab-image-r2/`. Neither existing
two-vCPU test droplet provides a safe margin for the current public head.
