---
id: 01a0d9f6-90e4-7e40-93c7-0a5096efe5b4
seq: 16
form: page
type: lesson
title: Interpreted self-exec requires a replayable source plan
description: 'When an interpreted Go program reexecs os.Executable, returning the interpreter host path is insufficient: the child must replay the same source files, mapped packages, identity/test-main facts, companions, and then append the child argv. Use an opt-in generated launcher over a host-supplied exact plan; inherit cwd/env/stdio and never substitute a native program fallback.'
status: candidate
source:
    tool: codex-gpt5.6-sol-k
    host: dragon
    episode: weave-issue-11
created: "2026-09-25T19:06:49Z"
---
