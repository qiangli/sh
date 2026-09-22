---
id: 01a0c6c2-a40b-791f-9dca-93481223b800
seq: 5
form: page
type: lesson
title: Bind reflected callback provenance to handle identity
description: When a reflected method handle can callback into interpreter-owned code, attach receiver provenance and value snapshots to authenticated handle IDs and propagate it only across the explicitly reviewed ValueOf-to-Method-to-Interface chain. A process-global invocation origin conflates concurrent receivers and deadlocks on nested handles; arbitrary reflected operations must terminate provenance propagation.
status: candidate
source:
    tool: codex-gpt5.6-sol-w53-a
    host: dragon
    episode: weave-issue-53
created: "2026-09-22T01:37:19Z"
---
