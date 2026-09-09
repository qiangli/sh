---
id: 2260cf1b608d
kind: task
title: 'Sprint 118: repair interpreted native argument type identity'
seq: 59
status: assigned
priority: p0
created: 2026-09-09T23:54:19.237249Z
assignee: sprint118-manager
sprint: 118
---

Candidate023 has one interpreted failure classified native-argument-type-identity at examples/recursion/recursion.go while oracle and compiled pass. Diagnose from retained raw evidence and implement the smallest shared gosource/lowering/runtime correction. Preserve unchanged source bodies; do not edit corpus classifications/normalizers or special-case the fixture/output. Add focused negative-first coverage for named recursive function arguments crossing the native boundary, then gate focused tests and go test ./gosource ./lower. Required trailers: Sprint #118 and this Story/Story-ID.
