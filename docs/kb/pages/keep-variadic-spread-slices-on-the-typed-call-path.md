---
id: 01a0c6a7-13e0-76fd-a73e-bd21a008041c
seq: 4
form: page
type: lesson
title: Keep variadic spread slices on the typed call path
description: When a Go-source call result is consumed as a value, route ellipsis calls through the same typed argument transporter as ordinary calls. Keep the spread slice as one provenance cell and set the spread marker; evaluating it through the legacy scalar path stringifies []T, loses backing-array aliasing, and can turn the enclosing call into a zero-result failure.
status: candidate
source:
    tool: codex-gpt5.6-sol-w43-q
    episode: weave-issue-43
created: "2026-09-22T01:07:13Z"
---
