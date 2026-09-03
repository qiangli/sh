---
id: "914698500017"
kind: task
title: Parse Bash++ short declarations and Go-form calls safely
seq: 8
status: done
priority: p0
created: 2026-09-03T10:02:04.404805Z
weave: 16
assignee: qiangli
sprint: 98
closed: 2026-09-03T11:57:23.067596Z
---

Child tranche of umbrella Story #165 (2ab74db14831). Implement := for both recorded start classes and Go-form calls using full supported-shape recognition before divergence. Preserve ordinary Bash for malformed/unsupported/Class-E near misses, with positions, semicolon handling inside committed regions, POSIX on/off, 1-byte readers, compatibility corpus exact-shape licenses, Walk/Printer/typedjson and source round trips. Must feed lexical scope and evaluator paths.
