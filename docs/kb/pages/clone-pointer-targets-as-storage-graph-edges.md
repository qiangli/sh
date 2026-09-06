---
type: lesson
title: Clone pointer targets as storage graph edges
description: When interpreted pointers can live in cells or nested structured values, snapshot cloning must publish each cloned cell before following pointer targets and memoize pointer objects. Rebind targets to cloned cells while preserving shared pointer identity; otherwise self-reference recurses, aliases split, or child tasks mutate parent storage.
status: validated
evidence: Pointer alias, subshell, and task snapshot tests plus the required syntax/interp gate passed on Sprint 116 Story 201.
source:
    tool: codex-gpt5.6-sol-q
    host: dragon
    episode: weave-issue-17
created: "2026-09-06T22:32:18Z"
updated: "2026-09-06T22:32:26Z"
---
