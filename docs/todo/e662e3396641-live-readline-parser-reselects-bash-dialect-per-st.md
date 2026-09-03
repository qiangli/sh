---
id: e662e3396641
kind: task
title: Live readline parser reselects Bash++ dialect per statement
seq: 10
status: done
priority: p0
created: 2026-09-03T12:06:33.473053Z
sprint: 98
closed: 2026-09-03T12:10:13.799064Z
---

Submodule delivery for umbrella/Bashy activation Story #196 (a615e2a6ad77). Extend interactive.Run with a live language selector so regular readline sessions choose syntax before each statement, including same-line set -o/+o bashpp transitions. Preserve classic default, prompts/history/completion, POSIX composition, cancellation, and leak/race behavior. Prove with real PTY enable/disable tests. This story owns only sh/interactive API and tests; Bashy CLI wiring remains in its own story.
