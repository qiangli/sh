---
id: 7b1702fc2428
kind: task
title: S192.3 Permit the documented go fence qualifier
seq: 127
status: assigned
priority: p0
created: 2026-09-15T13:02:46.118213Z
assignee: codex-gpt-5.5
sprint: 192
---

Blocking Sprint 192 fix: the approved dag contract uses ~~~go as go and go.launch(), but Go-keyword validation left that contextual alias inert and the lowerer could not emit it as a Go identifier. Admit only the go-fence/go-alias pair, keep go reserved everywhere else, and mangle the receiver in lowered output. Gate syntax, interpreted qualified call, and interpreted/lowered parity.
