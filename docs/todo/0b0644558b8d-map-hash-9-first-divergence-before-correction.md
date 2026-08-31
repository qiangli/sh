---
id: 0b0644558b8d
kind: task
title: Map hash:9 first divergence before correction
seq: 4
status: todo
priority: p0
created: 2026-08-31T23:33:22.020951Z
assignee: s88-getconf-hash
sprint: 88
---

Investigate hash:9 using only public-safe metadata, Issue 7 authority,
source/history, and short native reducers. Patch only with a mapped first
divergence; otherwise retain unresolved.

2026-08-31 investigation: current Profile D and both retained controls record
UNRESOLVED. The public metadata does not map this identity to a hash operation
or first observable. Current source covers the Issue 7 list, remember, reset,
lookup-failure, non-external-name, subshell-isolation, and standard-input seams;
the focused native hash suite passes. No standards-aligned shell correction is
justified from the shared numeric result.

Required redacted replay tuple: operation class (list, remember, reset,
lookup-failure, or other), result phase, shell/provider path and executable
digest by arm, POSIX-mode flag, effective PATH digest, operand category only
(external, builtin, function, slash-name, absent), pre/post cache cardinality,
numeric exit status, stdout/stderr byte counts and digests, and first differing
observable category. No operand text, output, journal text, or suite material.
