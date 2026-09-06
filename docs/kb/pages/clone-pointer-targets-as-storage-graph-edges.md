---
type: lesson
title: Clone pointer targets as storage graph edges
description: When interpreted pointers can live in cells or nested structured values, snapshot cloning must publish each cloned cell before following pointer targets and memoize pointer objects. Rebind targets to cloned cells while preserving shared pointer identity; otherwise self-reference recurses, aliases split, or child tasks mutate parent storage.
status: validated
evidence: Sprint 116 Story 201 slice 4 commit 88c6c206 preserved pointer identity by comparing storage target/path and left clonePointer memoization intact while adding composite equality/range coverage.
source:
    tool: codex-gpt5.6-sol-q
    host: dragon
    episode: weave-issue-17
created: "2026-09-06T22:32:18Z"
updated: "2026-09-06T22:50:40Z"
---
