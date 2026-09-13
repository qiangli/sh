---
id: 54313fa7cf4d
kind: task
title: 'Sprint 165.5: diagnostics — nul1 verdict, unclassified → empty by manifest, source-preserving front end DESIGN'
seq: 101
status: doing
priority: p1
created: 2026-09-13T09:07:05.140246Z
sprint: 165
---

Input: Barrier C active-154 + active-unclassified. Landed in 162: import-path rules as gc (canonical/absolute/empty/invalid char), NUL/invalid-UTF-8 verdict, type-check after gc's non-syntax parser diagnostics on gc's tree, anonymous types in expression position, language-version cause wording. By ID (source-preserving front end needed): issue23586, issue4468, issue50372. Remaining: nul1 (the verdict is missing/wording — read errorCheck's output, not the partition class), the unclassified set → EMPTY by manifest move (owners already assigned in gosource/testdata/sprint162/diag/FINDINGS.md — the harness lane moves them by rule or explicit manifest commit), and a DESIGN note with a number for the source-preserving front end (gcsyntax tree → go/ast, or go/types over gc's tree) that would close the three by-ID rows. RULES: exact upstream Go 1.27 harness is the authority; harness/tests Go or Bash only; product fixes = general Go mechanisms in sh/{gosource,interp,lower} from outside-corpus reproducers under <seam>/testdata/sprint165/<mechanism>/ with a DRIVING TEST and the negative set; never keyed to a fixture/expected string; never permissive; never a timeout raise; never edit sh/gosource/internal/gcsyntax; keep Options.CheckAfterSyntaxErrors. Rows recorded by ID in #162 (D1–D7) are never relabeled. Verify: focused go test -count=1, then the FULL sh gate go test -short ./interp/ ./lower/ ./gosource/ ./syntax/ (lower's parity tests are the classic-isolation gate; 8 GoSource* interp tests are pre-existing darwin failures), gofmt, git diff --check; then a subset leaf — workers ship a bundle + root TSV, ONLY the manager submits (leaf-submit.sh on sprint162-leaf). Trailers Sprint: #165 / Story: #<seq> / Story-ID as the last paragraph.
