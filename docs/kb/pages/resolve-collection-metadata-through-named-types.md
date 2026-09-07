---
type: lesson
title: Resolve collection metadata through named types
description: When runtime collection metadata retains a declared named or instantiated generic type, never assert meta.typ directly to a collection node. Resolve the underlying type for shape operations while preserving meta.typ as the declared identity; otherwise indexing or slicing a value returned by make can panic.
status: validated
evidence: Story 204 generic Vec[int] make/slice test and exact syntax/typedjson/interp gate passed on 2026-09-06.
source:
    tool: codex:gpt5.6-sol-c
    host: dragon
    episode: weave-issue-29
created: "2026-09-07T02:24:43Z"
updated: "2026-09-07T02:24:49Z"
---
