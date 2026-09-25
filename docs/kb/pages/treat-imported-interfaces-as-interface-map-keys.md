---
id: 01a0d63e-3ee9-7ed4-a5f9-1de4c5640a23
seq: 10
form: page
type: lesson
title: Treat imported interfaces as interface map keys
description: When GoSource map key types are imported named interfaces, admit them through the interface projection already backed by native export metadata and encode keys via the existing interface dynamic-type path. Do not blanket-accept unresolved named types; non-interface imported names still need concrete comparable storage identity.
status: candidate
source:
    tool: codex-gpt-5.5-x
    host: dragon
    episode: weave-issue-24
created: "2026-09-25T01:46:38Z"
---
