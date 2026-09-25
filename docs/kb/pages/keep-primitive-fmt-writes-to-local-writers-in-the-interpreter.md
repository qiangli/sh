---
id: 01a0da76-ca1d-7979-8594-2e1b72661b56
seq: 21
form: page
type: lesson
title: Keep primitive fmt writes to local writers in the interpreter
description: When GoSource code calls fmt.Fprint/Fprintln/Fprintf with an interpreter-owned writer and only builtin scalar operands, format locally and invoke the original Write body in the interpreter instead of round-tripping through the dependency helper. This avoids per-write callback transport in dump/printing loops while still falling back for values whose formatting could invoke user methods.
status: candidate
source:
    tool: codex-gpt-5.5-s
    host: dragon
    episode: weave-issue-19
created: "2026-09-25T21:26:53Z"
---
