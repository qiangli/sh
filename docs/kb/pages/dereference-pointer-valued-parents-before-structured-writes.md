---
type: lesson
title: Dereference pointer-valued parents before structured writes
description: When an evaluator builds an address path for chained Go selectors, instantiated generic receivers may leave a pointer as the parent of the final field even though Go inserts an implicit dereference. Follow the pointer storage identity before mutation and guard map or sequence assertions so unsupported shapes diagnose instead of panicking the host interpreter.
status: validated
evidence: TestGoSourceGbERangeIteratorsInterpreted and all TestGoSourcePointerField cases pass after following the pointer-valued assignment parent; the pre-fix host type assertion panic is gone.
source:
    tool: codex-gpt5.6-sol-y
    host: dragon
    episode: weave-issue-129
created: "2026-09-12T05:19:15Z"
updated: "2026-09-12T05:19:20Z"
---
