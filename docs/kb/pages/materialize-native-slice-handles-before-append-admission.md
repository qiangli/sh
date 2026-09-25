---
id: 01a0d659-b7ec-70bf-bff8-905dc145640a
seq: 11
form: page
type: lesson
title: Materialize native slice handles before append admission
description: When a Go-source builtin append operand comes from a dependency call or native selector, it may arrive as a bashPPBridgeValue handle with Type []T and no collection metadata. Materialize that handle into interpreter sequence storage before append's slice check; keep opaque native carriers rejected unless they have interpreted storage, so this does not become a native append fallback.
status: candidate
source:
    tool: codex-gpt-5.5-b
    host: dragon
    episode: weave-issue-28
created: "2026-09-25T02:16:39Z"
---
