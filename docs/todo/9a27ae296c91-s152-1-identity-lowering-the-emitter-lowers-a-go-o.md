---
id: 9a27ae296c91
kind: task
title: S152.1 identity lowering — the emitter lowers a Go-only input to itself (asmcheck residue)
seq: 77
status: done
priority: p0
created: 2026-09-10T21:18:20.954959Z
sprint: 152
closed: 2026-09-12T13:58:52.243317Z
---

RE-BOUND 2026-09-12 (plan step 5, leaf-152r1b): this card owns the emitter half of decision D1 — a Go-only input lowers to itself — and the asmcheck residue it drives. Landed on sh master 074f29aa (run 132): C2 sinks, C4 parentheses, C5 main, C6 guard prologue, C7 deref, C9 plain assertions, C10 names, the lower half of C1 (directives) and the C11 layout parts; lower/fidelity_test.go asserts byte-identity per closed class (C4/C6/C7/C10 closed; C1/C2/C3/C5/C8/C9/C11 open only because their reproducers still carry converter classes). Harness side (bashpp-tests, S152.0): H1 listing normalization, file-argument asm build. Active roots after run 1b: 11 asmcheck roots (C1-converter 5, C3 2, 4 to re-attribute on candidate 3: append, comparisons, issue60324, memops). Exit: every asmcheck root in leaf-152r1b/active-152-manifest.tsv passes compiled mode on the next candidate, or is moved to a named owner by manifest commit with its mechanism.

--- original card ---
Packet 152.1 has 156 baseline roots; manifest SHA-256 `78ada61bd174f93d2953b8039250de7e502ba1fc04fd6c32dd71e631c90b6fb4`, root-list SHA-256 `93178d845d3fac8e792fb4199787e19b7e0f2f1ce96f98b9009d5f35bbda2d45`. Do not dispatch before Barrier A binds an active replacement. Evidence: `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-152.1.json,final-causal-v2.json}`. `testdir:alias1.go` and `testdir:chan/nonblock.go` show compiled `transpile` exit 2 under `lowering.compiled_frontend_or_build_failure` (and currently also expose interpreted rejection).

Reduce a root to an outside-corpus source that reproduces the exact transpile stage, then add a negative/nearby supported form. The card owns the active manifest's lowering outcomes, not filenames; the named Lowering Emitter Integration Owner alone lands shared compiler/emitter seams. Verify minimal reproducers, authenticated 3–20-root subset, full active leaf, deterministic emitted bytes, affected `go test -count=1` packages, `go test -short ./...`, and PASS canaries. Every required mode must pass without delegating the original program or hiding a downstream failure.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.
