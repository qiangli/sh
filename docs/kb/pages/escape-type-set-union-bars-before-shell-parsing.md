---
type: gotcha
title: Escape type-set union bars before shell parsing
description: When Bash++ generic constraints use union terms in command-position declarations, an unescaped | is shell pipeline syntax before the Bash++ declaration recognizer sees the command. Source/printer forms must use an escaped pipe at the shell layer while normalizing to | before Go type-expression parsing.
status: candidate
source:
    tool: codex-gpt-5.5-z
    host: dragon
    episode: weave-issue-26
created: "2026-09-07T01:33:40Z"
---
