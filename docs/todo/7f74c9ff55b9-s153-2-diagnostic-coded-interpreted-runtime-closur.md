---
id: 7f74c9ff55b9
kind: task
title: S153.2 diagnostic-coded interpreted runtime closure
seq: 82
status: todo
priority: p0
created: 2026-09-10T21:18:21.102117Z
sprint: 153
---

Packet 153.2 has 31 baseline roots; manifest SHA-256 `5c6ae945b9c5217b7a61df5f3ab730ebf8bb2157b69954c5c300fa2e81f64958`, root-list SHA-256 `c79a0bad3847eaa05a675a63e551b9e8859e734d64d4e06f9dc3c48cf595e25e`. Barrier A active membership is authoritative. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-153.2.json,final-causal-v2.json}`. `testdir:bigalg.go` and `testdir:copy.go` are native PASS but interpreted `run` exit 1 under `runtime.interpreted_execution_failure`.

Reproduce the real runtime operation outside the corpus with a successful and failing/edge form; a diagnostic code is triage evidence, not permission to edit Sprint 154 policy. The card owns active-leaf behavior, not files. Shared runtime dispatch lands only through the named Runtime/Shellrt Integration Owner. Verify minimal reproducers, authenticated 3–20-root subset, full leaf twice, exact exit/output/process cleanup, affected tests with `-count=1`, `go test -short ./...`, and PASS canaries. Expected-failure inversion, matcher relaxation, and fallback are forbidden.
