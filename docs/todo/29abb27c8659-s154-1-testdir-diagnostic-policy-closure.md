---
id: 29abb27c8659
kind: task
title: S154.1 testdir diagnostic-policy closure
seq: 87
status: todo
priority: p0
created: 2026-09-10T21:18:21.244052Z
sprint: 154
---

Packet 154.1 has 158 baseline roots; manifest SHA-256 `18d8b2e3a8b27c0aa5f0e256ac1c847154cb7dcd4c56b4cc3c757d7b22172481`, root-list SHA-256 `48ed468cc383f4e8ae23a296f3614a5da2f72594c42758f0a0fe9e585aa576e1`. Its Barrier A active replacement is authoritative and final adjudication waits for Sprint 151/152 integration. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-154.1.json,final-causal-v2.json}`. `testdir:bombad.go` and `testdir:directive.go` executed `errorcheck` but mismatched under `diagnostic.executed_fixture_mismatch`.

Reduce one fixture to an outside-corpus invalid source with one expected diagnostic; add missing, extra, duplicate, wrong-line, and wrong-wording negatives. The card owns active-leaf policy outcomes, not files. Sprint 148 owns capture/remap/matcher primitives, and only named Execution Substrate/Diagnostic Policy integrators edit shared seams. Verify reproducers, authenticated subset/full leaf, exact applicability/status/text/multiplicity/original position, affected tests with `-count=1`, `go test -short ./...`, and PASS canaries. Matching must fail closed; permissive normalization and inversion are forbidden.
