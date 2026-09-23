---
id: 3d2e744afe4f
kind: task
title: S250 eliminate Windows bridge jitter from main-task time.Sleep
seq: 142
status: done
priority: p1
created: 2026-09-23T12:51:15.012233Z
weave: 238
assignee: codex-s250
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-23T18:18:32.478148Z
closed_by: codex-s250
---

Investigation, 2026-09-23: the unmerged local-sleep patch in weave #238 was
cross-built with Go 1.27.1 (candidate SHA-256
`13b1ba6b96692d8a05d48c62b3a465188893aa1346de078c5c469d4a0ab164f6`).
The worker log shows a following Windows default-selection sample with
`default_count=9`, `tick_count=5`, and one boom, still outside the native
default-count range `10..10`. The log lacks a per-row binary digest and a
matched repeated old-versus-patched receipt, so this is a warning against
accepting the patch, not a final root-cause proof. An earlier Go 1.26 build
failed an export-data version check and is not a valid comparison. Weave #238
was stopped; its isolated patch remains unmerged. Preserve the original Tour
source, comparator, and deadlines. A next attempt needs SHA-bound matched
Windows rows and focused output, callback, and cancellation controls.

Resolution, 2026-09-23: sh master
`69eda1b3d7c53ae15c004e529d20faeeea93caa3` contains the guarded
main-task `time.Sleep`, default-select and pure-time reply barrier changes,
plus local resolution of the authenticated `time.Millisecond` constant and
scalar `time.Duration.Round`. The original Tour source, semantic comparator,
and 60-second step limit were unchanged. Focused full-tag tests for local
sleep and cancellation, pure reply selection, duration values, select-default
output order, and native output interleaving passed.

Windows bridge profiling measured 28 `time.Millisecond` gets in the unchanged
default-selection source: 13.779 ms in requests plus 2.766 ms in output
barriers. A clean matched baseline returned nine defaults in all five runs;
the pure-duration candidate returned ten in all 35 runs, with its tenth
default at 477–496 ms. The integrated current-head binary returned ten in
all ten additional direct runs, with its tenth default at 479–487 ms. The
authenticated focused executor row passed baseline, interpreted, and
compiled modes against seven native oracle runs. Its partial ledger was
intentionally ineligible for the full gate.

Final public-head Windows Go Tour acceptance used Bashy
`f93816e28551a7f61193a3ee47312bccf653af67`, sh
`69eda1b3d7c53ae15c004e529d20faeeea93caa3`, and harness
`9379584eeaa20a230259fec78c7b53b6f7d22519`. The authenticated
candidate binary SHA-256 was
`36960b3320a29aa2f4727fd80234b3d7cba964960ec4047cf3202256d4a969b0`;
manifest SHA-256 was
`592892ffdca85e12091e401ffb46a197e34594afa922c7de51f44fa60e9b3f31`.
The unchanged full executor and independent validator both exited zero:
**291/291 PASS** (97 baseline, 97 interpreted, 97 compiled), with 11 semantic
rows and 77 native oracle runs. Evidence root:
`f5028238568575293266cb2c18b8eeb3f78bd58e0412e91f6d8f2e21cf5106d6`.
Ledger SHA-256:
`0389185d1346073c1c1ed1c8fbf0e754b8071a7fb4b7a1bfd4401290b049e0d4`.
Run-log SHA-256:
`de39d607e3f904a965501412e56ecca5ed346645b8a71e9fd31d52c3459c7e8e`.
Independent validator-log SHA-256:
`ac343ced5e9b716837e021339eba62aa843f65ab1cd96eb1e631eff01ef5ae89`.
