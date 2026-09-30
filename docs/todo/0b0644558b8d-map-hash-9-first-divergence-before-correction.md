---
id: 0b0644558b8d
kind: task
title: Map hash:9 first divergence before correction
seq: 4
status: todo
priority: p0
created: 2026-08-31T23:33:22.020951Z
assignee: s88-getconf-hash
sprint: 100
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

## Review 2026-09-30 (steward)

- Status: unknown at today's pins. The UNRESOLVED result was measured at Sprint 85/88 pins (a month old). No hash:9 fix landed, but the `hash` path changed since: sh afd578cf (2026-09-26, "names an embedder serves in process are never hashed") plus the Windows PATHEXT/virtual-root resolution commits (d75cfc75, ed1814f5).
- Outdated: "current Profile D" and "retained controls" refer to Sprint 85/88 evidence; the assignee seat (s88-getconf-hash) no longer exists.
- Next step: do NOT patch. Wait for the fresh baseline full arm at the frozen candidate (Sprint 110 story f093f2d6bba7). If hash:9 is still non-PASS there, request the redacted tuple listed above from that run and map the first divergence; if it PASSes, close as resolved-by-remeasure citing the candidate digest.
- Acceptance: either a PASS at the frozen candidate, or a mapped first divergence with a focused native regression test and an exact replay PASS.
- Depends on: Sprint 110 (runner 1727b5446a3b, candidate ae9b5ef7e7e8, baseline arm f093f2d6bba7).
