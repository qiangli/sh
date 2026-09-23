---
id: 23d31846f263
kind: bug
title: S250 restore interpreted Go Tour image deadline margin
seq: 141
status: todo
priority: p0
created: 2026-09-23T11:36:53.923078Z
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

A further exact one-row diagnostic on the first droplet kept the same product,
source and 60s limit, adding only system-wide one-second `perf stat` sampling
under the leaf lock. The interpreted image happened to pass at 57.623s, just
2.377s below the limit. Its partial ledger SHA-256 is
`5e88a6d612e459b5433a2516c350fa3ab972a1df52a3ba8d564b22f820cd20b6`
at `/srv/sprint250/story141-linux-perf-head-8a68fab/`; the sampled counts are
bound by `perf-1s.txt` SHA-256
`cc568e7e9b476b02489145563f0043ea1862623009c9d9eb63fddc642dc3811a`.
During seconds 3–60, the host made about 2.19 million context switches,
546,000 read syscalls and 268,000 write syscalls, versus roughly 400 context
switches per idle second. The repeated synchronous callback exchange dominates
this fixture. A four-file second-connection prototype cut the local image case
only about 8% and failed a race control for concurrent/reentrant callbacks;
it was rejected without Linux promotion. No measured, small, sound patch yet
gives the required comfortable margin, so keep this story open.

Resumed bounded callback-transport diagnosis: the unchanged full-tag local
image control on current `sh@3efbaccb` passed in 12.12–12.57s on macOS and
counted 131,078 original method callbacks versus only seven outer bridge
requests. CPU profiling placed 6.37s of 10.16s sampled time under callback
reply socket writes; the repeated cross-process exchange is the dominant
cost. On the second authorized Linux droplet, an authenticated one-row
diagnostic of the earlier public `Bashy@8a68fab1`/`sh@4888f8d7` candidate
with `GOMAXPROCS=1` still timed out under the unchanged 60s image bound
(baseline and compiled passed); partial ledger SHA-256
`d64a18e5de4533993cd76c2382cdacc09f75368d2b302da9a3f9f44b54b1208a`
at `/srv/sprint250/story89-linux-head-8a68fab/go-tour-image-gmp1/`.
Changing only the scheduler setting is insufficient and is not a product fix.

An isolated second-socket prototype removed the worker's callback-reply
channel handoff while preserving interpreter-side callback serialization,
nested fallback, authentication, output order, cancellation, and panic
controls. Focused controls passed, but the original image remained about
12.6s locally, indistinguishable from current code; the prototype was
reverted, not promoted to Linux or merged. The next repair needs a measured
reduction in per-callback cross-process wakeups (for example a reviewed
shared-memory notification protocol with reentrancy, process-death and
cross-platform controls), followed by exact Linux/Windows image rows and
the unchanged full Tour gates. No fixture, comparator, or deadline changed.
