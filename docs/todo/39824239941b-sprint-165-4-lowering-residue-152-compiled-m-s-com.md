---
id: 39824239941b
kind: task
title: 'Sprint 165.4: lowering residue (152) — compiled -m, .s companions (D3(b)), nilptr3 fidelity; cgo by ID'
seq: 100
status: todo
priority: p1
created: 2026-09-13T09:07:05.115527Z
sprint: 165
---

Input: Barrier C active-152 + retained-compiled. Landed in 162: body-less pass-through (41/41 compiled with the gc-direct backend), grouped-spec //line, literal/struct/call layout lines, no synthesised main (bashy passes the package clause), pragma attachment across blank lines, library emission. Remaining: compiled -m residue (~5: read gc's notes on the generated vs original module), the .s-companion roots (6 generate-phase + 3 execute-phase: D3(b) — the authority assembles them natively; decide with the harness whether compile-only recipes with .s inputs are the backend's to satisfy), nilptr3 comment-in-expression fidelity (152 C1), cgo 13 by ID (D4). Ledger: lower/testdata/sprint162/{lower-bodyless,lower-2}/FINDINGS.md. RULES: exact upstream Go 1.27 harness is the authority; harness/tests Go or Bash only; product fixes = general Go mechanisms in sh/{gosource,interp,lower} from outside-corpus reproducers under <seam>/testdata/sprint165/<mechanism>/ with a DRIVING TEST and the negative set; never keyed to a fixture/expected string; never permissive; never a timeout raise; never edit sh/gosource/internal/gcsyntax; keep Options.CheckAfterSyntaxErrors. Rows recorded by ID in #162 (D1–D7) are never relabeled. Verify: focused go test -count=1, then the FULL sh gate go test -short ./interp/ ./lower/ ./gosource/ ./syntax/ (lower's parity tests are the classic-isolation gate; 8 GoSource* interp tests are pre-existing darwin failures), gofmt, git diff --check; then a subset leaf — workers ship a bundle + root TSV, ONLY the manager submits (leaf-submit.sh on sprint162-leaf). Trailers Sprint: #165 / Story: #<seq> / Story-ID as the last paragraph.
