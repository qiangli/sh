---
id: b865191cf6e7
kind: chore
title: Review preserved Windows time.Sleep bridge weave draft
seq: 155
status: todo
priority: p2
created: 2026-09-24T02:53:00.195765Z
sprint: 266
sprint_id: 16738595-c072-5838-be1b-603932c6df8e
sprint_title: Bashy shell, Coreutils, and BashSharp follow-up after v0.28.0
---

Review killed sh weave #238 in ~/.bashy/weave/sh-7e2e7b65/workspaces/issue-238 before disposition. Its branch has no commits and three staged files: interp/bashpp_native_exit.go, interp/gosource_channel_make.go, interp/gosource_time_sleep_internal_test.go (152 added lines). Current sh main independently contains goSourceLocalTimeSleep and duration handling and shipped Windows Tour 291/291; compare exact semantics and tests before deciding supersession or focused salvage. Isolation violation and dirty index remain; do not pull, abandon, or clean workspace before steward review.
