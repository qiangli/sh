---
id: 01a0d5e5-d6b5-7b1f-8a24-8f671f05a96d
seq: 9
form: page
type: lesson
title: GNU Bash confirm harness needs per-case startup HOME
description: When running sh interp TestRunnerRunConfirm against independent GNU Bash, reject bashy on PATH, use REQUIRE_SHELLS=1, and choose comparator startup HOME from a leading literal HOME assignment or the temp dir; a created home child pollutes glob/dotglob cases, while a fixed HOME=/h breaks tilde cases that assign HOME inside the script. Build loadables with -dynamiclib on Darwin and -shared -fPIC elsewhere.
status: candidate
source:
    tool: codex-gpt-5.5-g
    host: dragon
    episode: weave-issue-7
created: "2026-09-25T00:10:04Z"
---
