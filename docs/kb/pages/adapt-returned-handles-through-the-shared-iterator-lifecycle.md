---
id: 01a1a73e-2a77-7860-a5c0-9bad1dcc518a
seq: 32
form: page
type: lesson
title: Adapt returned handles through the shared iterator lifecycle
description: When a foreign runtime returns an authenticated iterable handle, open it into the existing pull iterator and bounded range process instead of creating a parallel stream path. Keep StopIteration as EOF, close on every exit, classify only worker domain errors into an optional second binding, and suppress the same domain error if Close observes it again after Wait.
status: candidate
source:
    tool: codex-gpt5.6-sol-i
    episode: ep-9729c177ae578046
    parent_episode: ep-a4990ed872bca38d
created: "2026-11-04T15:47:11Z"
---
