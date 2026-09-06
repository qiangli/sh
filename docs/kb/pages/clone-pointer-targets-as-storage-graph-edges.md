---
type: lesson
title: Clone pointer targets as storage graph edges
description: When interpreted pointers can live in cells or nested structured values, snapshot cloning must publish each cloned cell before following pointer targets and memoize pointer objects. Rebind targets to cloned cells while preserving shared pointer identity; otherwise self-reference recurses, aliases split, or child tasks mutate parent storage.
status: validated
evidence: 'Story 201 closure kept pointer cells as storage edges while adding named pointer underlying-type identity, typed nil comparison, and new(named pointer) coverage; gate passed: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip ''TestParseConfirm|TestRunnerRunConfirm'' -count=1.'
source:
    tool: codex-gpt5.6-sol-q
    host: dragon
    episode: weave-issue-17
created: "2026-09-06T22:32:18Z"
updated: "2026-09-06T23:36:31Z"
---
