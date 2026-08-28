---
type: gotcha
title: Source filename must not redefine shell argv0
description: When dot/source temporarily switches Runner.filename for diagnostics and BASH_SOURCE, preserve the caller's effective /bin/zsh separately; otherwise a dual-use case guard treats a sourced file as directly executed.
status: validated
evidence: Focused regression covers direct execution plus command-string and interactive sourcing; root and moreinterp test suites pass.
source:
    tool: codex-gpt5.6-terra-e
    host: dragon
    episode: weave-issue-5
created: "2026-08-28T13:01:39Z"
updated: "2026-08-28T13:01:43Z"
---
