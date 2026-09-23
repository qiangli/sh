---
id: 3d2e744afe4f
kind: task
title: S250 eliminate Windows bridge jitter from main-task time.Sleep
seq: 142
status: doing
priority: p1
created: 2026-09-23T12:51:15.012233Z
weave: 238
assignee: qiangli
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
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
