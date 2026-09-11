---
id: 758fc8974a30
kind: task
title: S153.1 compiled runtime bridge closure
seq: 81
status: todo
priority: p0
created: 2026-09-10T21:18:21.072692Z
sprint: 153
---

Packet 153.1 has 15 baseline roots; manifest SHA-256 `229977c05481e4ee145e3bb68f6c828b89ab8be5932c4a58ad70cc33bf6e1844`, root-list SHA-256 `abcd021fd11b78e6ca5f88fa2539d214fe7346211b9b5c6d927e23084f7821a7`. Use only its Barrier A active replacement after relevant Sprint 151/152 interfaces integrate. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-153.1.json,final-causal-v2.json}`. `testdir:const3.go` and `testdir:devirtualization_nil_panics.go` show compiled `run` exit 2 under `runtime.compiled_execution_failure`.

Reduce a representative to an outside-corpus compiled executable that isolates live value/handle or panic behavior and add an interpreted parity control. This card owns active-leaf runtime outcomes, not filenames. Static declarations remain Sprint 151-owned, emitter changes Sprint 152-owned, and only the named Bridge Boundary/Lowering–Runtime integrators land shared seams. Verify the repro, authenticated 3–20-root subset, full leaf twice, exact process/output/exit evidence, affected `go test -count=1`, `go test -short ./...`, leak checks, and PASS canaries.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.
