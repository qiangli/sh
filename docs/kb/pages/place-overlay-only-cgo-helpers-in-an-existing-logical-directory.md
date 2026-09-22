---
id: 01a0caa9-8764-7e34-9451-05bfc996efef
seq: 8
form: page
type: lesson
title: Place overlay-only cgo helpers in an existing logical directory
description: 'When a generated GoSource worker adds cgo files through an overlay, give every virtual .go file a unique path in an existing module directory: cmd/cgo changes into the logical source directory and fails if an overlay-only parent directory does not exist. Keep physical bytes and artifacts in private scratch, enable CGO only for authenticated cgo metadata, and preserve one preamble per generated file.'
status: candidate
source:
    tool: codex-gpt5.6-sol-w213-e
    host: dragon
    episode: weave-issue-213
created: "2026-09-22T19:48:23Z"
---
