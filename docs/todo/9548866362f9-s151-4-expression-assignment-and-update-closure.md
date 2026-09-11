---
id: 9548866362f9
kind: task
title: S151.4 expression assignment and update closure
seq: 75
status: todo
priority: p0
created: 2026-09-10T21:18:20.901587Z
sprint: 151
---

Status: non-dispatchable seed/localization card until Barrier A. Packet 151.4 has 101 baseline roots; manifest SHA-256 `fa36ab3c5c733b8a9747188aa772afbe0df215e09d89c60a3dd91dd28edaf811`, root-list SHA-256 `da7a6262b74028bd38a0bd19695cf55bde6a0018f00d32ed0f8a5c43e3ea1be3`. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-151.4.json,final-causal-v2.json}`. Representatives `testdir:abi/leaf2.go` and `testdir:abi/part_live.go` are native PASS and interpreted `run` exit 2 under `type_evaluation.interpreted_rejection`.

Before Barrier A, authenticate and build outside-corpus reproducers that isolate operand/result typing, arity, multiple assignment, update, and blank-identifier behavior. After active binding, this card owns the manifest's semantic outcomes, with files selected only after localization. A named Sprint 151/Static Type integrator owns shared evaluator dispatch. Verify the repro, authenticated 3–20-root subset, full active leaf, affected package tests with `-count=1`, `go test -short ./...`, and PASS canaries. All required modes and exact native behavior must agree; no fixture keys, fallback, exclusion, or failure relabeling.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.

Closing artifact (docs/sprint-151-master-execution-plan.md, 2026-09-11): after Barrier A this card is RE-BOUND to one of the five largest decisive-rule clusters of the active Sprint 151 manifest (the family name above is a seed label, not an acceptance criterion). It closes on its active leaf's packet-gate run: exit 0, or exit 3 with every non-green root moved to a named owner (152/153/154) by a reviewed manifest commit — never an in-place relabel. Product fixes land in sh/gosource (converter owner) or sh/interp + one bashy CLI commit (bridge owner); every commit carries Sprint: #151 / Story: / Story-ID: trailers.
