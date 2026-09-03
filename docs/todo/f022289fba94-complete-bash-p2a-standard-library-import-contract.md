---
id: f022289fba94
kind: task
title: Complete Bash++ P2A standard-library import contract
seq: 11
status: done
priority: p0
created: 2026-09-03T12:15:46.997895Z
weave: 22
assignee: qiangli
sprint: 98
closed: 2026-09-03T12:27:46.271921Z
---

Child tranche of umbrella Story #165 (2ab74db14831). Replay only the reviewed
Issue #20 patch at 28a033dabcf3cbc4026cfd3dd6b9a4cf3a2e6b20 onto current
main, retaining its transactional import parser and AST while completing P2A:
runner-local standard-library imports, an injectable package-private evaluator
and exact Go-toolchain identity, atomic alias/path collision rules, explicit
Reset and subshell semantics, cancellation/race coverage, safe Go AST selector
calls, and complete printer/Walk/typedjson plus one-byte/classic/POSIX matrices.
Grouped, dot, blank, and local-package imports remain explicitly out of scope.
