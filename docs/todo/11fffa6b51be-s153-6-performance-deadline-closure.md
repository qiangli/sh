---
id: 11fffa6b51be
kind: task
title: S153.6 performance deadline closure
seq: 86
status: todo
priority: p0
created: 2026-09-10T21:18:21.219445Z
sprint: 153
---

Packet 153.6 has 19 baseline roots; manifest SHA-256 `23a74f033fa496226979b156932d69e61ce0d528e54c1357e6b248ec0c0f74f4`, root-list SHA-256 `2ac0227fa900406ebfebeb96195b7bd90b71f01a85ad40ae230801564a2603b2`. Barrier A active membership is authoritative. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-153.6.json,final-causal-v2.json}`. `testdir:abi/fibish.go` and `testdir:abi/fibish_closure.go` reached interpreted `run`, signal 9/state `deadline`, under `performance.authenticated_stage_deadline`.

First distinguish semantic nontermination from slow correct execution using an outside-corpus scaled input and terminal/complexity measurements. The card owns active-leaf performance outcomes, not files; the upstream 60-second bound cannot increase. Verify scaled reproducers, authenticated 3–20-root subset, full leaf twice under 60 seconds, CPU/memory/process evidence, affected tests/benchmarks with `-count=1`, `go test -short ./...`, leak checks, and PASS canaries. Timeout extension, work omission, and special casing are failures.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.
