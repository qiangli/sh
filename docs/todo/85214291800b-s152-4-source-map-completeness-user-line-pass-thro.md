---
id: 85214291800b
kind: task
title: S152.4 source-map completeness — user //line pass-through and column-0 directives
seq: 80
status: todo
priority: p0
created: 2026-09-10T21:18:21.041669Z
sprint: 152
---

RE-BOUND 2026-09-12 (plan step 5, leaf-152r1b): landed (run 134, sh 074f29aa): the emitter reproduces the user's line directive (filename and adjusted line) in its //line, emits the line-only form when the column is unknown (closes 'invalid column number: 0' on dwarf/dwarf.go, issue18149.go, issue22662.go, issue38698.go compiled mode), records both physical and adjusted positions in the map, and Result.ValidateMappings fails closed on a corrupted/truncated map. Remaining after run 1b: issue18149.go and issue22662.go INTERPRETED mode ('want /foo/bar.go:N', 'want ??:N') — the interpreter's runtime.Caller view of user line directives, a runtime concern to route to 153 by manifest commit at closure unless the interpreter owner claims it inside this sprint. Exit: no 'invalid column number' row on candidate 3; the two interpreted rows moved with their mechanism named.

--- original card ---
Packet 152.4 has 5 baseline roots; manifest SHA-256 `4e6997241957bf2b058e19f15d7b86bb737701c5e24a361e3818c3f4d8cfedd9`, root-list SHA-256 `a3baa519bcaf7949a72bc9d8e84b347ae2761b6d92e3f8653d10d7eb4587ca01`. Barrier A active membership is required. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-152.4.json,final-causal-v2.json}`. `testdir:dwarf/linedirectives.go` has absent/unsuccessful `run`; `testdir:fixedbugs/issue18149.go` has interpreted `run` exit 2, both classified `lowering.invalid_source_map`.

Reduce one case to an outside-corpus generated fragment with known original positions; corrupt/remove a map entry as the negative control. The card owns active-leaf map completeness/outcomes, not files. Sprint 154 owns diagnostic policy; shared changes go through the named integrator. Verify reproducers, authenticated subset/full leaf, generated-to-original positions, fail-closed malformed maps, affected `go test -count=1`, `go test -short ./...`, and PASS canaries in all required modes.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.
