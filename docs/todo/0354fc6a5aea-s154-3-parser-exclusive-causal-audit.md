---
id: 0354fc6a5aea
kind: task
title: S154.3 parser-exclusive causal audit
seq: 89
status: todo
priority: p0
created: 2026-09-10T21:18:21.299302Z
sprint: 154
---

Packet 154.3 has 0 baseline roots; manifest SHA-256 `4cbcf889d17fa9ac97b34ce7cc6beb6b2c9713889530beaaee94d1395f89565c`, empty root-list SHA-256 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`. It is a Barrier A mechanism/audit leaf, not implementation inventory. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-154.3.json,final-causal-v2.json}`; the baseline parser-exclusive count is exactly zero.

Authenticate that the regenerated leaf remains empty. If it does not, for every assigned root preserve the exact first failing stage and produce an outside-corpus syntax reproducer plus a nearby accepted form before any edit. The card owns causal audit and any proven active semantic outcome, not filenames; production parser/shared-policy changes require the named Diagnostic Policy Integrator. Verify the active leaf/index hashes and disjointness, focused parser tests with `-count=1`, `go test -short ./...`, all required modes, and PASS canaries. A syntax node in a diagnostic is not parser evidence, and relabeling cannot manufacture closure.
