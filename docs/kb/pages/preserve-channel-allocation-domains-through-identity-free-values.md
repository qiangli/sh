---
id: 01a0c94b-3069-728c-9a2f-81ccc12b058b
seq: 6
form: page
type: lesson
title: Preserve channel allocation domains through identity-free values
description: When Go-source channel planning certifies an interpreter-local domain, preserve that domain when a channel is assigned nil and when a channel value is rebound from collection metadata (for example a range over []chan T). A typed nil has no native handle authority, and a ranged channel must restore its channel/channelOwner sidecar; otherwise an all-local select is falsely classified as mixed or the operand loses its interpreted capability. Keep live mixed native/interpreted selects fail-closed.
status: candidate
source:
    tool: codex-gpt5.6-sol-w173-q
    host: dragon
    episode: weave-issue-173
created: "2026-09-22T13:25:43Z"
---
