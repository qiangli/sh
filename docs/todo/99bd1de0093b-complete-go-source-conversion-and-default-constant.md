---
id: 99bd1de0093b
kind: task
title: Complete Go source conversion and default constant lowering
seq: 53
status: done
priority: p0
created: 2026-09-09T06:37:55.781661Z
weave: 74
assignee: qiangli
sprint: 118
closed: 2026-09-09T18:18:37.665354Z
resolution: fixed
closed_by: claude-opus4.8-v
---

Sprint 118 continuation on current sh 037aaf8687e0aa049efb4f7a2dceb3a4941e9bbc. Read CLAUDE.md and /Users/qiangli/projects/poc/dhnt/docs/sprint-118-handoff.md. First bounded slice: locate the two retained importdecl0 failures in candidate013 checker evidence under /Users/qiangli/.bashy/sprint118, reproduce one against the authenticated Go 1.27 toolchain, fix their coherent gosource/lower frontend cause, and add focused unchanged-source regressions. Own gosource/* and lower frontend files only. Do not touch interp/native/concurrency files, frozen candidates, or run broad corpora. Use GOMAXPROCS=2 and GOFLAGS=-p=2. Commit with Sprint: #118, Story: #53, Story-ID: 99bd1de0093b trailers. Do not push, merge, close, or claim corpus closure. Parent 6f0c4d9a31be.
