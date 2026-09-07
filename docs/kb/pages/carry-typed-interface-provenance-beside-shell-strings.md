---
type: lesson
title: Carry typed interface provenance beside shell strings
description: When a typed value crosses an existing string-only Bash++ call path, keep dynamic interface identity on the lexical cell and pass only a narrow side channel for bare interface arguments. This preserves the scalar/composite/pointer value model while letting function parameters and snapshots retain dynamic type/value identity.
status: validated
evidence: Story 202 preserved interface dynamic type/value side-channel through embedded interface assignment/assertion and function-call paths; exact gate passed.
source:
    tool: codex-gpt-5.5-v
    host: dragon
    episode: weave-issue-22
created: "2026-09-06T23:59:25Z"
updated: "2026-09-07T00:40:14Z"
---
