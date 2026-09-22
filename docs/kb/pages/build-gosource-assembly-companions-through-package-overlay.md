---
id: 01a0ca59-557f-78cd-89d0-fad267c98424
seq: 7
form: page
type: lesson
title: Build GoSource assembly companions through package overlay
description: 'When a GoSource dependency helper must bind same-package .s companions, do not pass .s files to go build as named files: cmd/go rejects non-.go named files. Build the source package directory instead, with an overlay replacing the original Go root by the generated helper, so companion objects are included while original Go bodies stay out of the native build.'
status: candidate
source:
    tool: codex-gpt-5.5-w
    host: dragon
    episode: weave-issue-205
created: "2026-09-22T18:20:47Z"
---
