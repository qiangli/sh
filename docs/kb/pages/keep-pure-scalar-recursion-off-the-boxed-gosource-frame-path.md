---
id: 01a0d62b-1300-7a60-93be-02bcf2dbf870
seq: 9
form: page
type: lesson
title: Keep pure scalar recursion off the boxed GoSource frame path
description: When a checked GoSource function is side-effect-free and uses only local int expressions, compile the whole eligible body into an immutable interpreter plan and fall back before execution for every unsupported node; this removes per-call frames and cell copies without native fallback. For bridge comparisons, test canonical plain scalar equality before constructing evaluator request state, because request environment snapshots can dominate tight polling loops.
status: candidate
source:
    tool: codex-gpt5.6-sol-s
    host: dragon
    episode: weave-issue-19
created: "2026-09-25T01:25:42Z"
---
