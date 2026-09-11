---
id: 35b464edd335
kind: task
title: S152.3 generated-program lineage closure
seq: 79
status: todo
priority: p0
created: 2026-09-10T21:18:21.011961Z
sprint: 152
---

Packet 152.3 has 12 baseline roots; manifest SHA-256 `7c3f1539c020615533f47431e39ec5e27fc064d23ae1297d4542a1ef907c9a46`, root-list SHA-256 `b1700b6182981b101f0d5b9a74d7eb09ec92b5b64fdb78ea8a3af3c2e9657584`. Dispatch only from the Barrier A active manifest. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-152.3.json,final-causal-v2.json}`. `testdir:64bit.go` and `testdir:chan/select5.go` are `runoutput` roots with generated evidence under `lowering.generated_program_or_lineage_failure`.

Reproduce with the smallest outside-corpus generator whose child artifact/process cannot be joined, plus a clean single-child control. The card owns the active manifest's generated-denominator and association outcomes, not files; the named Execution Substrate Integrator owns shared fixes. Verify reproducers, authenticated 3–20-root subset, full leaf, affected `go test -count=1`, `go test -short ./...`, and PASS canaries. Every child, artifact, parent, exit/deadline, and reap must bind exactly once to an immutable root.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.
