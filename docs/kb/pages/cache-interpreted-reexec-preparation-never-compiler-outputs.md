---
id: 01a0e494-dab7-7b2b-9f4d-3f70ee236d4d
seq: 26
form: page
type: lesson
title: Cache interpreted reexec preparation, never compiler outputs
description: When a replacement Go tool reexecs an interpreted program, profile through entry to separate gosource load/lowering/index startup from the tool body. If preparation dominates, share only the serialized prepared program in the parent launcher's private lifetime, keyed by every root and mapped source byte, the exact interpreter binary, and Go target configuration; publish atomically under a cross-process single-flight lock. Never cache compiler outputs or key them only by argv/importcfg text, because referenced archives may change and cmd/go already owns artifact caching.
status: candidate
source:
    tool: aurelia-s88-n
    host: dragon
    episode: weave-issue-196
created: "2026-09-27T20:35:55Z"
---
