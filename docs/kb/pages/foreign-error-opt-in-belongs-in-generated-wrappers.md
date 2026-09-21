---
id: 01a0c25d-7a1e-7b08-8d6e-067b52ee1373
seq: 3
form: page
type: lesson
title: Foreign error opt-in belongs in generated wrappers
description: When a typed foreign Bash# call explicitly asks for a trailing error result, emit a generated helper beside the legacy wrapper and have call sites invoke that helper. Inlining calls to the private __bpp*_foreign* module inside lowered function/task bodies can fail lexical-value checking because the generated module binding is outside that narrow declaration/lowering view.
status: candidate
source:
    tool: codex-gpt-5.5-j
    host: dragon
    episode: weave-issue-10
created: "2026-09-21T05:08:21Z"
---
