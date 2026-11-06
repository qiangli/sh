---
id: 01a1a76d-4545-7dc7-ad28-14a358eeb74f
seq: 33
form: page
type: lesson
title: Keep callable and pull callbacks distinct on one reverse channel
description: When one foreign worker uses the shared host callback table for both general callables and pipeline input, give the capabilities distinct wire tags and proxies while retaining one registry and reverse-frame dispatcher. The callable proxy must drain capture before its reverse request and dispatch nested host requests while waiting, or re-entry deadlocks and callback-boundary output is duplicated or reordered.
status: candidate
source:
    tool: codex-gpt5.6-sol-l
    episode: ep-6bb74bfa759fd0b8
    parent_episode: ep-a4990ed872bca38d
created: "2026-11-04T16:38:38Z"
---
