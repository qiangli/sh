---
id: 01a0d96b-18bb-764d-afe8-6653a8944bdb
seq: 14
form: page
type: lesson
title: Pin GoSource runtime toolchain environment
description: When a GoSource run starts the native dependency/runtime helper with the default Go environment, pin the runtime env as well as the build env to the selected Go identity (GOROOT and GOTOOLCHAIN). Otherwise interpreted os/exec calls can pick up a different host go toolchain than the helper, changing compiler-package subprocess results without a command failure.
status: candidate
source:
    tool: codex-gpt-5.5-f
    host: dragon
    episode: weave-issue-6
created: "2026-09-25T16:34:29Z"
---
