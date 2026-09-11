---
id: 76bbbdd0ebd5
kind: task
title: S153.3 channel-path runtime closure
seq: 83
status: todo
priority: p0
created: 2026-09-10T21:18:21.129365Z
sprint: 153
---

Packet 153.3 has 2 baseline roots; manifest SHA-256 `363a65e828ede4b72a6b7ee2c7935b118cac3800aaabd100290490d0fb5716a6`, root-list SHA-256 `6351ea93a5328e1a80f93d453049cb7b3ce94185cd55ce4f1e533defb5f20446`. Dispatch only from its Barrier A active replacement after Sprint 151 channel typing integrates. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-153.3.json,final-causal-v2.json}`. Both `testdir:chan/powser2.go` and `testdir:chan/sieve2.go` are native PASS but interpreted `run` exit 1 under `runtime.interpreted_execution_failure`.

Create an outside-corpus minimal pipeline covering send/receive, close/cancellation, and goroutine completion plus a leak/deadlock negative. This card owns active-leaf channel runtime outcomes, not files; shared runtime seams go through the named Runtime/Shellrt Integration Owner. Verify reproducers, the exact active leaf twice, applicable race/package tests with `-count=1`, `go test -short ./...`, authenticated parent/terminal evidence, deterministic cancellation/reap, no surviving goroutine/process, and PASS canaries.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.
