---
id: 01a0d667-9994-728c-8be4-0742bb1402c2
seq: 12
form: page
type: lesson
title: Canonicalize owned pointer selector addresses
description: When GoSource takes an address through an interpreter-owned pointer field, such as &e.list.root, collapse the implicit selector dereference to the pointee storage path instead of storing a symbolic deref step. Otherwise a recomputed address and a previously stored field pointer name the same struct sentinel but compare unequal.
status: candidate
source:
    tool: codex-gpt-5.5-d
    host: dragon
    episode: weave-issue-30
created: "2026-09-25T02:31:48Z"
---
