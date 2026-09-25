---
id: 01a0da9b-966b-7af8-8ef4-2a2568b7d39a
seq: 23
form: page
type: lesson
title: Synchronize copied slices for bounded callbacks
description: 'When a reviewed synchronous GoSource callback consumer such as testing.T.Run carries direct original slice storage, reuse the callback-boundary slice synchronization instead of refusing: apply the dependency copy before the callback and refresh it after, while preserving refusals for unknown retainers, mutators, and nested unreconcilable slice views.'
status: candidate
source:
    tool: codex-gpt-5.5-a
    host: dragon
    episode: weave-issue-27
created: "2026-09-25T22:07:04Z"
---
