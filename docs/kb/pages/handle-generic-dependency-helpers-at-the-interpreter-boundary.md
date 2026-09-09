---
type: lesson
title: Handle generic dependency helpers at the interpreter boundary
description: When an unchanged Go source calls a generic stdlib dependency helper such as errors.AsType[T], the native bridge cannot reflect an uninstantiated generic function. Implement the reviewed semantic slice on the interpreter side, preserving original local bodies and letting the bridge handle only nongeneric dependency calls.
status: candidate
source:
    tool: codex-gpt-5.5-x
    host: dragon
    episode: weave-issue-76
created: "2026-09-09T18:36:27Z"
---
