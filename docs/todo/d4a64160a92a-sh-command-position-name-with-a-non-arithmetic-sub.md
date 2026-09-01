---
id: d4a64160a92a
kind: task
title: 'sh: command-position NAME[...] with a non-arithmetic subscript is rejected where bash 5.3 accepts a glob word'
seq: 4
status: todo
priority: p0
created: 2026-09-01T02:36:15.162372Z
assignee: lintel
sprint: 97
---

Handed to Sprint 88 for review per aurelia's request in room 8. Bash treats NAME[...] specially only as an ASSIGNMENT TARGET; without a following '=', f[int] is an ordinary word and the brackets are a glob class. hasValidIdent commits on p.r=='[' alone, so every NAME[ enters getAssign, whose else-branch parses the subscript as ARITHMETIC — making f[[]int], f[map[string]int], f[*T], f[[]] and f[[x]] hard parse errors where bash 5.3 accepts words. Reproduces under --posix, so it reaches the certification profile. Argument position unaffected. This story carries the failing SPEC, not a fix: three guard attempts were each defeated by the streaming, non-backtracking parser (chunk-boundary conservatism restores old behaviour; readEOF is false even when fully buffered; p.fill() would invalidate the scanned slice). A correct fix likely belongs where the arithmetic parse is entered, not in hasValidIdent.
