---
id: 01a0e846-2fa9-7d8c-a32f-11b8e0fc7720
seq: 29
form: page
type: lesson
title: Reuse callback transport policy for range-function yields
description: When a GoSource range iterator yields non-scalars, bind the callback cells with their value metadata instead of rendering them as strings. Admit copied structs/arrays and authenticated imported handles through the existing callback transport policy; fail closed before iterator execution for local reference-bearing slices, maps, pointers, funcs, channels, and interfaces whose aliases cannot cross safely.
status: candidate
source:
    tool: codex-gpt5.6-sol-e
    host: dragon
    episode: weave-issue-239
created: "2026-09-28T13:48:28Z"
---
