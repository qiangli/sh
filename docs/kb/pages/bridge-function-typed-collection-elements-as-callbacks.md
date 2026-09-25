---
id: 01a0da43-cdc2-7cfc-a6a0-1c1cf9ba43b4
seq: 19
form: page
type: lesson
title: Bridge function-typed collection elements as callbacks
description: When an interpreter-owned collection stores func-typed elements, bridge the stored closure handle through bashPPBridgeCell instead of scalar string transport. This preserves callback identity for map, slice, or struct fields later passed to dependency functions, such as compiler arch init maps.
status: candidate
source:
    tool: codex-gpt-5.5-o
    host: dragon
    episode: weave-issue-15
created: "2026-09-25T20:31:11Z"
---
