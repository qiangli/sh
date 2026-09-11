---
id: d76c1d1c0f11
kind: task
title: S154.2 go-types and types2 diagnostic-policy closure
seq: 88
status: todo
priority: p0
created: 2026-09-10T21:18:21.271995Z
sprint: 154
---

Packet 154.2 has 33 baseline roots; manifest SHA-256 `2c7ab2966653d3c9ec09696051a57c58f60d485f29c46039754b483508e48cd2`, root-list SHA-256 `0eb550e050ef09df81f92fabb7139d6eda862ade2506e1497f4f550ab067ff51`. Use only its Barrier A active replacement; final decisions follow Sprint 151/152. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-154.2.json,final-causal-v2.json}`. `cmd/compile/internal/types2:TestCheck/builtins0.go` and `go/types:TestCheck/builtins0.go` are native PASS typechecker roots under `diagnostic.executed_fixture_mismatch`.

Build an outside-corpus paired `go/types`/`types2` fixture with one expected positioned error plus missing/extra/wrong-position negatives. The card owns active-leaf policy outcomes, not files; shared changes land only through named diagnostic/substrate integrators. Verify reproducers, authenticated subset/full leaf, exact applicability/status/text/multiplicity/position in required modes, focused checker tests with `-count=1`, `go test -short ./...`, and PASS canaries. No permissive normalization or expected-failure inversion.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.
