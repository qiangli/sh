---
type: lesson
title: Separate value metadata from readonly identity
description: When typed structs and arrays share a JSON-shaped object model with slice/map references, keep each cell's value-layout metadata separate from the shared deep-readonly identity. Copy struct/array payload and metadata recursively while retaining nested slice/map references; at task or subshell snapshot boundaries deep-clone both payloads and metadata with memoization so alias graphs stay intact without cross-run mutation.
status: validated
evidence: 'Story 201 closure kept named collection meta.typ as named identity while resolving underlying collection shapes for operations; required gate passed: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip ''TestParseConfirm|TestRunnerRunConfirm'' -count=1.'
source:
    tool: codex-gpt5.6-sol-p
    host: dragon
    episode: weave-issue-16
created: "2026-09-06T22:10:30Z"
updated: "2026-09-06T23:20:25Z"
---
