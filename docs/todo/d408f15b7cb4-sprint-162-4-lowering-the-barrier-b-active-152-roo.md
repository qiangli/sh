---
id: d408f15b7cb4
kind: task
title: 'Sprint 162.4: lowering — the barrier-b/active-152 roots (19) + the 93 retained-COMPILED product FAILs (LOWER-EUNSUPPORTED body-less funcs, generate/execute phase, cgo policy)'
seq: 94
status: todo
priority: p0
created: 2026-09-13T00:37:02.745334Z
sprint: 162
---

Input: barrier-b/active-152-manifest.tsv + the compiled rows of barrier-b/active-retained-manifest.tsv, re-measured by S162.0. Under Sprint 155 D3 a compiled-mode failure is a product FAIL whatever the partition calls it — the 'retained' owner only exempts INTERPRETED compiler-artifact rows. Classes: 152 (19): 6 interpreted asmcheck pattern rows + 4 interpreted optimizer rows (verify these are truly interpreted-only → zero credit, else fix), compiled 'can inline __gosource_pkg…' mangled-name rows (5; D1 of Sprint 152: a Go-only input lowers to itself — the emitter must keep original names for single-package inputs), LOWER-ETYPE declared-and-not-used (2). Retained compiled (93): LOWER-EUNSUPPORTED function declarations without body (~19 roots; the emitter must pass body-less declarations through as Go does — assembly-backed funcs are a compile-time fact, not an error), 'Bash++ backend unsupported generate phase: compile input outside the working copy' (6) and 'unsupported execute phase: module package' (3) — seam dispositions the backend must implement or the product must satisfy, and cgo (import "C", runtime/cgo: ~11 roots) — a pure-Go product cannot satisfy these; write the honest disposition as a recorded design decision on the card (they stay FAIL until the user decides; do not exclude them yourself). Exit: every non-cgo root PASSES compiled; cgo roots carry a recorded decision. RULES: fixes are general Go mechanisms in sh (gosource / interp / lower), each from an OUTSIDE-CORPUS reproducer with the negative set; never keyed to a fixture path or expected string; never a permissive comparison; never a timeout raise. Verify: focused go test -count=1, go test -short ./..., git diff --check (judge by NEW darwin failures only; never in a weave --verify), then the story leaf on sprint162-leaf (one coordinator; never the certification host, never the dev box) plus nearby native-PASS canaries. Rows move by manifest commit, never in place. Commit trailers Sprint: #162 / Story: S162.<n> / Story-ID: <id> as the last paragraph.
