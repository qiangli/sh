---
id: 01a0da85-3b03-713c-bc48-fd96acdab611
seq: 22
form: page
type: lesson
title: Batch mapped companion validation before bridge build
description: When GoSource starts a dependency bridge with many mapped assembly companion packages, do not run cmd/go list once per package to verify generated-helper and SFiles selection. Overlay every package first, then run one go list over all mapped package import paths and validate each returned record; otherwise large package maps such as cmd/compile/internal/ssa can burn startup time before any interpreted test event.
status: candidate
source:
    tool: codex-gpt-5.5-w
    host: dragon
    episode: weave-issue-23
created: "2026-09-25T21:42:39Z"
---
