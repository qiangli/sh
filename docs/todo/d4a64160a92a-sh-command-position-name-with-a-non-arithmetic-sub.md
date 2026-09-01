---
id: d4a64160a92a
kind: task
title: 'sh: command-position NAME[...] with a non-arithmetic subscript is rejected where bash 5.3 accepts a glob word'
seq: 4
status: done
priority: p0
created: 2026-09-01T02:36:15.162372Z
assignee: lintel
sprint: 88
---

Handed to Sprint 88 for review per aurelia's request in room 8. Bash treats NAME[...] specially only as an ASSIGNMENT TARGET; without a following '=', f[int] is an ordinary word and the brackets are a glob class. hasValidIdent commits on p.r=='[' alone, so every NAME[ enters getAssign, whose else-branch parses the subscript as ARITHMETIC — making f[[]int], f[map[string]int], f[*T], f[[]] and f[[x]] hard parse errors where bash 5.3 accepts words. Reproduces under --posix, so it reaches the certification profile. Argument position unaffected. Three lookahead guards were rejected because they violated the streaming parser's chunk contract.

Resolved in Sprint 88 at the arithmetic-assignment entry/recovery boundary. A command-position candidate is parsed permissively until its closing bracket. If no `=` follows, it is re-lexed as one ordinary word, preserving glob and expansion semantics; if `=` follows, a recorded invalid-arithmetic condition remains a parse error. Real indexed assignments continue to produce `CallExpr.Assigns`. Focused coverage includes all reported shapes, invalid assignment counterparts, printer/word semantics, and one-byte-reader streaming.
