---
id: ffb8e756b553
kind: task
title: Parse Bash++ type declarations with exact Class-E fallback
seq: 7
status: assigned
priority: p0
created: 2026-09-03T10:02:04.385092Z
weave: 9
assignee: qiangli
sprint: 98
---

Child tranche of umbrella Story #165 (2ab74db14831). After safe var/const parser lands, parse supported Go-spelling type declarations and aliases into typed AST with exact start-site classification/positions, Walk/Printer/typedjson coverage, POSIX on/off, 1-byte readers, and non-destructive fallback for every unsupported or shell-valid near miss. Include generic type parameter surface only when its grammar is implemented; do not claim arbitrary type bodies.
