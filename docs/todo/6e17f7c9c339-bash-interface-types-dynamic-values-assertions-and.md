---
id: 6e17f7c9c339
kind: task
title: Bash++ interface types, dynamic values, assertions, and type switches
seq: 32
status: done
priority: p2
created: 2026-09-06T23:44:30.981382Z
assignee: qiangli
sprint: 116
closed: 2026-09-07T00:10:56.726354Z
---

Integrated bounded interface foundation in 006d20d0 plus semantic corrections in 18a43ed0. Delivered positioned AST and parser/printer/Walk/typed-JSON coverage; named and anonymous interface declarations; concrete dynamic values; nil-interface versus typed-nil distinction; one-result fatal and comma-ok assertions with typed zero values; type switches; method-set satisfaction checks; function/subshell/task provenance; and correct value-copy versus pointer-identity behavior. Exact Sprint 116 gate and clean-room review pass. Remaining before this child can close: direct calls through interface values; readonly enforcement through interface-held aliases; ordinary Go method-spec source spelling; static impossible-assertion rejection; and any uncovered fallback/streaming acceptance cases. Embedding, promoted members, and final receiver method-set closure remain the next Story 202 slice.
