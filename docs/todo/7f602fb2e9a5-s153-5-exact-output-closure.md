---
id: 7f602fb2e9a5
kind: task
title: S153.5 exact output closure
seq: 85
status: todo
priority: p0
created: 2026-09-10T21:18:21.184578Z
sprint: 153
---

Packet 153.5 has 11 baseline roots; manifest SHA-256 `88dd53fc57b9f8c2c5bd9079366a0ca1e199252bdea069a467908c35f57e72c9`, root-list SHA-256 `a7593e4e8d83ddfd090610bac179c847ff9e682126e90759739711c9e1574e78`. Use its Barrier A active replacement and the accepted ordered-capture API. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-153.5.json,final-causal-v2.json}`. `testdir:fixedbugs/bug352.go` and `testdir:fixedbugs/issue12577.go` completed execution but failed native-output comparison under `output.completed_execution_mismatch`.

Reduce one mismatch to an outside-corpus program preserving the differing bytes/order and add a stdout/stderr interleaving negative. This card owns active-leaf language output outcomes, not files; matching policy remains with Sprint 154 and changes only through named integrators. Verify reproducers, authenticated subset/full leaf twice, byte-exact ordered streams bound to exit/deadline/process identity, negative capture tests, affected `go test -count=1`, `go test -short ./...`, and PASS canaries.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.
