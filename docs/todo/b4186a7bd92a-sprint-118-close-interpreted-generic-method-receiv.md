---
id: b4186a7bd92a
kind: task
title: 'Sprint 118: close interpreted generic method receiver cluster'
seq: 58
status: assigned
priority: p0
created: 2026-09-09T23:01:20.732937Z
assignee: sprint118-manager
sprint: 118
---

Candidate023 has exactly two interpreted failures classified generic-method-receiver: examples/generics/generics.go and examples/range-over-iterators/range-over-iterators.go; oracle and compiled pass. Diagnose from retained evidence under /Users/qiangli/.local/state/bashy/sprint118-evidence/runtime-integration-023 and implement the smallest shared gosource/lowering/runtime correction in sh. Preserve unchanged original bodies, avoid corpus/normalizer edits, and add focused negative-first regression tests proving generic receiver binding/type arguments without special-casing paths or output. Gate focused tests plus go test ./gosource ./lower as appropriate. Commit with exact Sprint/Story/Story-ID trailers and submit for manager review.
