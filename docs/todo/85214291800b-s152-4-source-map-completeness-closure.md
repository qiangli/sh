---
id: 85214291800b
kind: task
title: S152.4 source-map completeness closure
seq: 80
status: todo
priority: p0
created: 2026-09-10T21:18:21.041669Z
sprint: 152
---

Packet 152.4 has 5 baseline roots; manifest SHA-256 `4e6997241957bf2b058e19f15d7b86bb737701c5e24a361e3818c3f4d8cfedd9`, root-list SHA-256 `a3baa519bcaf7949a72bc9d8e84b347ae2761b6d92e3f8653d10d7eb4587ca01`. Barrier A active membership is required. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-152.4.json,final-causal-v2.json}`. `testdir:dwarf/linedirectives.go` has absent/unsuccessful `run`; `testdir:fixedbugs/issue18149.go` has interpreted `run` exit 2, both classified `lowering.invalid_source_map`.

Reduce one case to an outside-corpus generated fragment with known original positions; corrupt/remove a map entry as the negative control. The card owns active-leaf map completeness/outcomes, not files. Sprint 148 owns remap primitives and Sprint 154 diagnostic policy; shared changes go through their named integrator. Verify reproducers, authenticated subset/full leaf, generated-to-original positions, fail-closed malformed maps, affected `go test -count=1`, `go test -short ./...`, and PASS canaries in all required modes.
