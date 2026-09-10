---
id: e492792d1a74
kind: task
title: S152.2 generated-Go compile and build closure
seq: 78
status: todo
priority: p0
created: 2026-09-10T21:18:20.985496Z
sprint: 152
---

Packet 152.2 has 113 baseline roots; manifest SHA-256 `b2b49927e66536c32d17dd8e25d94002fca85c4b5e1248d7ffd635f72894f712`, root-list SHA-256 `87c89ae509a209c8134dba11f888a472da9e580529fe4798edc6241616b2d79d`. Barrier A active membership supersedes it. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-152.2.json,final-causal-v2.json}`. `testdir:abi/bad_internal_offsets.go` and `testdir:fixedbugs/bug057.go` are native PASS but compiled `compile` exit 2 under `lowering.compiled_frontend_or_build_failure`.

Retain the generated Go for one representative, reproduce the bound-SDK compiler failure, and reduce it to an outside-corpus generator input plus supported neighbor. This card owns active-leaf generated import/type/ABI outcomes, not files. Runtime implementation remains Sprint 153-owned; shared ABI changes land only through the named Lowering–Runtime Integrator. Verify reproducers, authenticated 3–20-root subset, full leaf, artifact/importcfg/sdk identity, affected `go test -count=1`, `go test -short ./...`, and PASS canaries. All modes must pass and generated compilation must fail closed on identity drift.
