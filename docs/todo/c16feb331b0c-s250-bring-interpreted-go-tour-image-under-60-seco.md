---
id: c16feb331b0c
kind: bug
title: S250 bring interpreted Go Tour image under 60 seconds on DO Linux
seq: 137
status: todo
priority: p0
created: 2026-09-23T09:43:53.068118Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

The authenticated older Linux Tour run has 290/291 observations with interpreted _content/tour/solutions/image.go killed at the unchanged 60-second deadline; prior focused current sh@6e458ae8 timing on DO was 61.095 seconds. Reproduce on the current candidate, fix the interpreter performance cause, keep semantics and the 60-second limit, and rerun the targeted case and full Go Tour 291 on DO; check macOS regression. Preserve the unmerged /tmp/s250-tour-sh draft unless reviewed. Sprint 250 Story #676.
